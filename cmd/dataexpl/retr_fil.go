package main

import (
	"bytes"
	"context"
	"fmt"
	"github.com/filecoin-project/cidtravel/ctbstore"
	"github.com/filecoin-project/go-address"
	"github.com/ipfs/go-blockservice"
	"github.com/ipfs/go-cid"
	"github.com/ipfs/go-merkledag"
	basicnode "github.com/ipld/go-ipld-prime/node/basic"
	"io"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/gorilla/mux"
	offline "github.com/ipfs/go-ipfs-exchange-offline"
	format "github.com/ipfs/go-ipld-format"
	"github.com/ipld/go-ipld-prime/codec/dagjson"
	"github.com/ipld/go-ipld-prime/traversal/selector/builder"
	"golang.org/x/xerrors"

	"github.com/filecoin-project/go-fil-markets/retrievalmarket"
	"github.com/filecoin-project/go-state-types/big"

	lapi "github.com/filecoin-project/lotus/api"
	bstore "github.com/filecoin-project/lotus/blockstore"
	"github.com/filecoin-project/lotus/chain/types"
	cliutil "github.com/filecoin-project/lotus/cli/util"
)

type selGetter func(ss builder.SelectorSpec) (cid.Cid, format.DAGService, map[string]struct{}, func(), error)

func (h *dxhnd) getCarFilRetrieval(r *http.Request, ma address.Address, pcid, dcid cid.Cid) func(ss builder.SelectorSpec) (io.ReadCloser, error) {
	return func(ss builder.SelectorSpec) (io.ReadCloser, error) {
		vars := mux.Vars(r)

		sel, err := pathToSel(vars["path"], false, ss)
		if err != nil {
			return nil, err
		}

		eref, done, err := h.retrieveFil(r.Context(), nil, ma, pcid, dcid, &sel, nil)
		if err != nil {
			return nil, xerrors.Errorf("retrieve: %w", err)
		}
		defer done()

		eref.DAGs = append(eref.DAGs, lapi.DagSpec{
			DataSelector:      &sel,
			ExportMerkleProof: true,
		})

		rc, err := cliutil.ClientExportStream(h.ainfo.Addr, h.ainfo.AuthHeader(), *eref, true)
		if err != nil {
			return nil, err
		}

		return rc, nil
	}
}

func (h *dxhnd) getFilRetrieval(bsb *ctbstore.TempBsb, r *http.Request, ma address.Address, pcid, dcid cid.Cid) selGetter {
	return func(ss builder.SelectorSpec) (cid.Cid, format.DAGService, map[string]struct{}, func(), error) {
		vars := mux.Vars(r)

		sel, err := pathToSel(vars["path"], false, ss)
		if err != nil {
			return cid.Undef, nil, nil, nil, xerrors.Errorf("filretr: %w", err)
		}

		tbs, err := bsb.MakeStore()
		if err != nil {
			return cid.Cid{}, nil, nil, nil, xerrors.Errorf("make temp store: %w", err)
		}

		var cbs bstore.Blockstore = tbs
		bbs := ctbstore.NewBlocking(bstore.Adapt(cbs))
		cbs = bbs

		storeid, err := h.apiBss.MakeRemoteBstore(context.TODO(), ctbstore.NewCtxWrap(cbs, ctbstore.WithNoBlock))
		if err != nil {
			if err := tbs.Release(); err != nil {
				log.Errorw("release temp store", "error")
			}
			return cid.Cid{}, nil, nil, nil, err
		}

		eref, done, err := h.retrieveFil(r.Context(), &storeid, ma, pcid, dcid, &sel, func() {
			bbs.Finalize()
			if err := tbs.Release(); err != nil {
				log.Errorw("release temp store", "error")
			}

			log.Warnw("store released")
		})
		if err != nil {
			if err := tbs.Release(); err != nil {
				log.Errorw("release temp store", "error")
			}
			return cid.Undef, nil, nil, nil, xerrors.Errorf("retrieve: %w", err)
		}

		eref.DAGs = append(eref.DAGs, lapi.DagSpec{
			DataSelector:      &sel,
			ExportMerkleProof: true,
		})

		bs := bstore.NewTieredBstore(cbs, bstore.NewMemory())
		ds := merkledag.NewDAGService(blockservice.New(bs, offline.Exchange(bs)))

		rs, err := SelectorSpecFromPath(Expression(vars["path"]), false, ss)
		if err != nil {
			return cid.Cid{}, nil, nil, nil, xerrors.Errorf("failed to parse path-selector: %w", err)
		}

		root, links, err := findRoot(r.Context(), dcid, rs, ds)
		if err != nil {
			return cid.Cid{}, nil, nil, nil, xerrors.Errorf("find root: %w", err)
		}

		return root, ds, links, done, err
	}
}

func (h *dxhnd) listenRetrievalUpdates(ctx context.Context) {
topLoop:
	for {
		if ctx.Err() != nil {
			log.Warnw("stopping retrieval updates", "ctx", ctx.Err())
			return
		}

		subscribeEvents, err := h.api.ClientGetRetrievalUpdates(ctx)
		if err != nil {
			log.Warnw("retrieval updates", "error", err)
			time.Sleep(time.Second)
			continue
		}

		for {
			select {
			case <-ctx.Done():
				continue topLoop
			case event, ok := <-subscribeEvents:
				if !ok {
					continue topLoop
				}

				h.filRetrPs.Pub(event, "events")
			}
		}
	}
}

func (h *dxhnd) retrieveFil(ctx context.Context, apiStore *lapi.RemoteStoreID, minerAddr address.Address, pieceCid, file cid.Cid, sel *lapi.Selector, retrDone func()) (*lapi.ExportRef, func(), error) {
	payer, err := h.api.WalletDefaultAddress(ctx)
	if err != nil {
		return nil, nil, err
	}

	var offer lapi.QueryOffer
	{ // Directed retrieval
		offer, err = h.api.ClientMinerQueryOffer(ctx, minerAddr, file, &pieceCid)
		if err != nil {
			return nil, nil, xerrors.Errorf("offer: %w", err)
		}
	}
	if offer.Err != "" {
		// todo record too
		return nil, nil, fmt.Errorf("offer error: %s", offer.Err)
	}

	maxPrice := big.Zero()
	//maxPrice := big.NewInt(6818260582400)

	if offer.MinPrice.GreaterThan(maxPrice) {
		return nil, nil, xerrors.Errorf("failed to find offer satisfying maxPrice: %s (min %s, %s)", maxPrice, offer.MinPrice, types.FIL(offer.MinPrice))
	}

	o := offer.Order(payer)
	o.DataSelector = sel
	o.RemoteStore = apiStore

	ctx, cancel := context.WithCancel(ctx)

	subscribeEvents := h.filRetrPs.Sub("events")
	retrievalRes, err := h.api.ClientRetrieve(ctx, o)
	if err != nil {
		cancel()
		return nil, nil, xerrors.Errorf("error setting up retrieval: %w", err)
	}

	log := log.With("id", retrievalRes.DealID, "miner", minerAddr, "file", file, "piece", pieceCid)
	log.Infow("retrieval started")

	// todo re-sub on id-topic specific channel

	start := time.Now()
	resCh := make(chan error, 1)
	var resSent bool

	go func() {
		defer func() {
			h.filRetrPs.Unsub(subscribeEvents, "events")
			if retrDone != nil {
				retrDone()
			}
		}()
		defer cancel()

		for {
			var evt lapi.RetrievalInfo
			select {
			case <-ctx.Done():
				if !resSent {
					go func() {
						err := h.api.ClientCancelRetrievalDeal(context.Background(), retrievalRes.DealID)
						if err != nil {
							log.Errorw("cancelling deal failed", "error", err)
						}
					}()
				}

				resCh <- xerrors.New("Retrieval Timed Out")

				log.Infow("retrieval done", "reason", "timed out", "duration", time.Since(start))
				return
			case evti := <-subscribeEvents:
				evt = evti.(lapi.RetrievalInfo)
				if evt.ID != retrievalRes.DealID {
					// we can't check the deal ID ahead of time because:
					// 1. We need to subscribe before retrieving.
					// 2. We won't know the deal ID until after retrieving.
					continue
				}
			}

			event := "New"
			if evt.Event != nil {
				event = retrievalmarket.ClientEvents[*evt.Event]
			}

			fmt.Printf("Recv %s, Paid %s, %s (%s), %s\n",
				types.SizeStr(types.NewInt(evt.BytesReceived)),
				types.FIL(evt.TotalPaid),
				strings.TrimPrefix(event, "ClientEvent"),
				strings.TrimPrefix(retrievalmarket.DealStatuses[evt.Status], "DealStatus"),
				time.Now().Sub(start).Truncate(time.Millisecond),
			)

			switch evt.Status {
			case retrievalmarket.DealStatusOngoing:
				if apiStore != nil && !resSent {
					resCh <- nil
					resSent = true
				}
			case retrievalmarket.DealStatusCompleted:
				if !resSent {
					resCh <- nil
				}
				log.Infow("retrieval done", "reason", "completed", "duration", time.Since(start))
				return
			case retrievalmarket.DealStatusRejected:
				if !resSent {
					resCh <- xerrors.Errorf("Retrieval Proposal Rejected: %s", evt.Message)
				}
				log.Infow("retrieval done", "reason", "rejected", "duration", time.Since(start))
				return
			case
				retrievalmarket.DealStatusDealNotFound,
				retrievalmarket.DealStatusErrored:
				if !resSent {
					resCh <- xerrors.Errorf("Retrieval Error: %s", evt.Message)
				}
				log.Infow("retrieval done", "reason", "errored/not found", "duration", time.Since(start))
				return
			}
		}
	}()

	err = <-resCh
	if err != nil {
		return nil, nil, err
	}

	eref := &lapi.ExportRef{
		Root:   file,
		DealID: retrievalRes.DealID,
	}
	return eref, func() {
		err := h.api.ClientCancelRetrievalDeal(context.Background(), retrievalRes.DealID)
		if err != nil {
			log.Errorw("cancelling deal failed", "error", err)
		}
	}, nil
}

func pathToSel(psel string, matchTraversal bool, sub builder.SelectorSpec) (lapi.Selector, error) {
	rs, err := SelectorSpecFromPath(Expression(psel), matchTraversal, sub)
	if err != nil {
		return "", xerrors.Errorf("failed to parse path-selector: %w", err)
	}

	var b bytes.Buffer
	if err := dagjson.Encode(rs.Node(), &b); err != nil {
		return "", err
	}

	fmt.Println(b.String())

	return lapi.Selector(b.String()), nil
}

// PathValidCharset is the regular expression fully matching a valid textselector
const PathValidCharset = `[- _0-9a-zA-Z\/\.~]`

// Expression is a string-type input to SelectorSpecFromPath
type Expression string

var invalidChar = regexp.MustCompile(`[^` + PathValidCharset[1:len(PathValidCharset)-1] + `]`)

func SelectorSpecFromPath(path Expression, matchPath bool, optionalSubselectorAtTarget builder.SelectorSpec) (builder.SelectorSpec, error) {
	/*

	   Path elem parsing
	   * If first char is not `~`, then path elem is unixfs
	   * If first char is `~`
	   	* If second char is not `~` then path[1:] is ipld dm
	   	* If second char is `~`
	   		* If third char is `~` then path is unixfs path[2:]
	   		* If third char is `i` then path is "~"+path[3:]


	   /some/path -> ufs(some)/ufs(path)

	   /~cb/~pa/file -> /cb/pa/ufs(file)

	   /~~~ -> /ufs(~)

	   /~~i -> /~

	*/

	if path == "/" {
		return nil, fmt.Errorf("a standalone '/' is not a valid path")
	} else if m := invalidChar.FindStringIndex(string(path)); m != nil {
		return nil, fmt.Errorf("path string contains invalid character at offset %d", m[0])
	}

	ssb := builder.NewSelectorSpecBuilder(basicnode.Prototype.Any)

	ss := optionalSubselectorAtTarget
	// if nothing is given - use an exact matcher
	if ss == nil {
		ss = ssb.Matcher()
	}

	segments := strings.Split(string(path), "/")

	// walk backwards wrapping the original selector recursively
	for i := len(segments) - 1; i >= 0; i-- {
		seg := segments[i]

		if seg == "" {
			// allow one leading and one trailing '/' at most
			if i == 0 || i == len(segments)-1 {
				continue
			}
			return nil, fmt.Errorf("invalid empty segment at position %d", i)
		}

		seg, isUnix, err := decodeSegment(seg)
		if err != nil {
			return nil, err
		}

		if seg == "" {
			return nil, fmt.Errorf("invalid empty segment at position %d", i)
		}

		if seg == "." || seg == ".." {
			return nil, fmt.Errorf("unsupported path segment '%s' at position %d", seg, i)
		}

		ss = ssb.ExploreFields(func(efsb builder.ExploreFieldsSpecBuilder) {
			efsb.Insert(seg, ss)
		})

		if isUnix {
			ss = ssb.ExploreInterpretAs("unixfs", ss)
		}

		if matchPath {
			ss = ssb.ExploreUnion(ssb.Matcher(), ss)
		}
	}

	return ss, nil
}

func decodeSegment(seg string) (string, bool, error) {
	isUnix := true

	if len(seg) == 0 {
		return "", false, nil
	}

	if seg[0] == '~' {
		if len(seg) < 2 {
			return "", false, xerrors.Errorf("path segment prefixed with ~ must be longer than 3 characters")
		}

		if seg[1] == '~' {
			if len(seg) < 3 {
				return "", false, xerrors.Errorf("path segment prefixed with ~~ must be longer than 3 characters")
			}
			switch seg[2] {
			case '~':
				seg = seg[2:]
			case 'i':
				if len(seg) < 4 {
					return "", false, xerrors.Errorf("path segment prefixed with ~~i must be longer than 4 characters")
				}

				isUnix = false
				seg = "~" + seg[3:]
			default:
				return "", false, xerrors.Errorf("unknown segment mode '%c'", seg[2])
			}
		} else {
			isUnix = false
			seg = seg[1:]
		}
	}

	return seg, isUnix, nil
}

func ipldToPathSeg(is string) string {
	if is == "" { // invalid, but don't panic
		return ""
	}
	if is[0] != '~' {
		return "~" + is
	}
	if len(is) < 2 { // invalid but panicking is bad
		return "~~i"
	}

	return "~~i" + is[1:]
}

func unixToPathSeg(is string) string {
	if is == "" { // invalid, but don't panic
		return ""
	}
	if is[0] != '~' {
		return is
	}
	if len(is) < 2 { // invalid but panicking is bad
		return "~~~"
	}

	return "~~~" + is[1:]
}

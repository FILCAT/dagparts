package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"github.com/filecoin-project/go-address"
	"github.com/filecoin-project/go-state-types/builtin"
	"github.com/filecoin-project/index-provider/metadata"
	cliutil "github.com/filecoin-project/lotus/cli/util"
	"github.com/ipfs/go-cid"
	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/multiformats/go-multiaddr"
	"github.com/multiformats/go-multihash"
	"golang.org/x/exp/constraints"
	"html/template"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"sync"

	"github.com/gorilla/mux"
	"github.com/ipld/go-ipld-prime/node/basicnode"
	"github.com/ipld/go-ipld-prime/traversal/selector"
	"github.com/ipld/go-ipld-prime/traversal/selector/builder"
	"golang.org/x/xerrors"

	"github.com/filecoin-project/go-fil-markets/storagemarket"
	"github.com/filecoin-project/go-state-types/abi"
	"github.com/filecoin-project/go-state-types/builtin/v9/market"

	"github.com/filecoin-project/lotus/chain/types"
)

func (h *dxhnd) handleMiners(w http.ResponseWriter, r *http.Request) {
	pstat, err := h.trackerFil.AllProviderPingStats()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	rstat, err := h.trackerFil.AllProviderRetrievalStats()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	tpl, err := template.New("providers.gohtml").Funcs(map[string]any{
		"sizeClass": sizeClass,
	}).ParseFS(dres, "dexpl/providers.gohtml")
	if err != nil {
		fmt.Println(err)
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html")
	w.WriteHeader(http.StatusOK)
	data := map[string]interface{}{
		"miners":    h.mminers,
		"ping":      pstat,
		"retrieval": rstat,
	}
	if err := tpl.Execute(w, data); err != nil {
		fmt.Println(err)
		return
	}
}

func (h *dxhnd) handleDeals(w http.ResponseWriter, r *http.Request) {
	deals, err := h.api.ClientListDeals(r.Context())
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	tpl, err := template.ParseFS(dres, "dexpl/client_deals.gohtml")
	if err != nil {
		fmt.Println(err)
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html")
	w.WriteHeader(http.StatusOK)
	data := map[string]interface{}{
		"deals":             deals,
		"StorageDealActive": storagemarket.StorageDealActive,
	}
	if err := tpl.Execute(w, data); err != nil {
		fmt.Println(err)
		return
	}
}

func (h *dxhnd) handleClients(w http.ResponseWriter, r *http.Request) {
	type clEntry struct {
		Addr  address.Address
		Count int64
		Data  string
	}

	var clEnts []clEntry
	cf, err := os.OpenFile(filepath.Join(h.clientMeta, "clients.json"), os.O_RDONLY, 0666)
	if err != nil {
		fmt.Println(err)
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if err := json.NewDecoder(cf).Decode(&clEnts); err != nil {
		fmt.Println(err)
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	cf.Close()

	tpl, err := template.ParseFS(dres, "dexpl/clients.gohtml")
	if err != nil {
		fmt.Println(err)
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html")
	w.WriteHeader(http.StatusOK)

	data := map[string]interface{}{
		"clients": clEnts,
	}
	if err := tpl.Execute(w, data); err != nil {
		fmt.Println(err)
		return
	}
}

func (h *dxhnd) handleClient(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	ma, err := address.NewFromString(vars["id"])
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	type provMeta struct {
		Prov address.Address
		Data string
		Deal abi.DealID
	}

	clDeals := make([]provMeta, 0)

	cf, err := os.OpenFile(filepath.Join(h.clientMeta, fmt.Sprintf("cl-%s.json", ma)), os.O_RDONLY, 0666)
	if err != nil {
		fmt.Println(err)
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if err := json.NewDecoder(cf).Decode(&clDeals); err != nil {
		fmt.Println(err)
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	cf.Close()

	providers := map[address.Address]int{}
	for _, d := range clDeals {
		providers[d.Prov]++
	}

	tpl, err := template.ParseFS(dres, "dexpl/client.gohtml")
	if err != nil {
		fmt.Println(err)
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html")
	w.WriteHeader(http.StatusOK)

	data := map[string]interface{}{
		"deals":     clDeals,
		"addr":      ma,
		"providers": intoList(providers),
	}
	if err := tpl.Execute(w, data); err != nil {
		fmt.Println(err)
		return
	}
}

func intoList[K address.Address, V constraints.Ordered](in map[K]V) []struct {
	K K
	V V
} {
	var out []struct {
		K K
		V V
	}

	for k, v := range in {
		out = append(out, struct {
			K K
			V V
		}{K: k, V: v})
	}

	sort.Slice(out, func(i, j int) bool {
		return out[i].V > out[j].V
	})

	return out
}

type DealInfo struct {
	DealCID cid.Cid
	Client  address.Address
	Filplus bool
	Size    string
}

func (h *dxhnd) handleProviderSectors(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	vars := mux.Vars(r)
	ma, err := address.NewFromString(vars["id"])
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	ms, err := h.api.StateMinerSectors(ctx, ma, nil, types.EmptyTSK)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	head, err := h.api.ChainHead(ctx)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	mp, err := h.api.StateMinerPower(ctx, ma, head.Key())
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	var deals []abi.DealID
	for _, info := range ms {
		for _, d := range info.DealIDs {
			deals = append(deals, d)
		}
	}

	commps := map[abi.DealID]DealInfo{}
	var wg sync.WaitGroup
	wg.Add(len(deals))
	var lk sync.Mutex

	for _, deal := range deals {
		go func(deal abi.DealID) {
			defer wg.Done()

			md, err := h.api.StateMarketStorageDeal(ctx, deal, types.EmptyTSK)
			if err != nil {
				return
			}

			if md.Proposal.Provider == address.Undef {
				return
			}

			lk.Lock()
			commps[deal] = DealInfo{
				DealCID: md.Proposal.PieceCID,
				Client:  md.Proposal.Client,
				Filplus: md.Proposal.VerifiedDeal,
				Size:    types.SizeStr(types.NewInt(uint64(md.Proposal.PieceSize))),
			}

			lk.Unlock()
		}(deal)
	}
	wg.Wait()

	// filter out inactive deals
	for _, m := range ms {
		filtered := make([]abi.DealID, 0, len(m.DealIDs))
		for _, d := range m.DealIDs {
			if _, found := commps[d]; found {
				filtered = append(filtered, d)
			}
		}
		m.DealIDs = filtered
	}

	now, err := h.api.ChainHead(r.Context())
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	mi, err := h.api.StateMinerInfo(r.Context(), ma, now.Key())
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	multiaddrs := make([]multiaddr.Multiaddr, 0, len(mi.Multiaddrs))
	for i, a := range mi.Multiaddrs {
		maddr, err := multiaddr.NewMultiaddrBytes(a)
		if err != nil {
			log.Warnf("parsing multiaddr %d (%x): %s", i, a, err)
			continue
		}
		multiaddrs = append(multiaddrs, maddr)
	}

	if mi.PeerId != nil {
		multiaddrs, err = peer.AddrInfoToP2pAddrs(&peer.AddrInfo{
			ID:    *mi.PeerId,
			Addrs: multiaddrs,
		})
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
	}

	tpl, err := template.New("sectors.gohtml").Funcs(map[string]interface{}{
		"EpochTime": func(e abi.ChainEpoch) string {
			return cliutil.EpochTime(now.Height(), e)
		},
	}).ParseFS(dres, "dexpl/sectors.gohtml")
	if err != nil {
		fmt.Println(err)
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html")
	w.WriteHeader(http.StatusOK)
	data := map[string]interface{}{
		"maddr":   ma,
		"sectors": ms,
		"deals":   commps,
		"info":    mi,
		"addrs":   multiaddrs,

		"qap": types.SizeStr(mp.MinerPower.QualityAdjPower),
		"raw": types.SizeStr(mp.MinerPower.RawBytePower),
	}
	if err := tpl.Execute(w, data); err != nil {
		fmt.Println(err)
		return
	}
}

func (h *dxhnd) handleDeal(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	did, err := strconv.ParseInt(vars["id"], 10, 64)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	ctx := r.Context()

	d, err := h.api.StateMarketStorageDeal(ctx, abi.DealID(did), types.EmptyTSK)
	if err != nil {
		http.Error(w, xerrors.Errorf("StateMarketStorageDeal: %w", err).Error(), http.StatusInternalServerError)
		return
	}

	lstr, err := d.Proposal.Label.ToString()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	dcid, err := cid.Parse(lstr)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	lstr = dcid.String()
	d.Proposal.Label, _ = market.NewLabelFromString(lstr) // if it's b64, will break urls

	var cdesc string

	{
		// get left side of the dag up to typeCheckDepth
		g := h.getFilRetrieval(h.tempBsBld, r, d.Proposal.Provider, d.Proposal.PieceCID, dcid)

		ssb := builder.NewSelectorSpecBuilder(basicnode.Prototype.Any)
		root, dserv, _, done, err := g(ssb.ExploreRecursive(selector.RecursionLimitDepth(typeCheckDepth),
			ssb.ExploreUnion(ssb.Matcher(), ssb.ExploreFields(func(eb builder.ExploreFieldsSpecBuilder) {
				eb.Insert("Links", ssb.ExploreIndex(0, ssb.ExploreFields(func(eb builder.ExploreFieldsSpecBuilder) {
					eb.Insert("Hash", ssb.ExploreRecursiveEdge())
				})))
			})),
		))
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		defer done()

		// this gets type / size / linkcount for root

		desc, _, err := linkDesc(ctx, root, "", dserv)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		cdesc = desc.Desc

		if desc.Size != "" {
			cdesc = fmt.Sprintf("%s %s", cdesc, desc.Size)
		}
	}

	now, err := h.api.ChainHead(r.Context())
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	resp, err := h.idx.Find(ctx, dcid.Hash())
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	bitswapProvs := map[peer.ID]int{}
	filProvs := map[peer.ID]int{}

	for _, result := range resp.MultihashResults {
		for _, providerResult := range result.ProviderResults {
			var meta metadata.Metadata
			if err := meta.UnmarshalBinary(providerResult.Metadata); err != nil {
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}

			for _, proto := range meta.Protocols() {
				switch meta.Get(proto).(type) {
				case *metadata.GraphsyncFilecoinV1:
					filProvs[providerResult.Provider.ID]++
				case *metadata.Bitswap:
					bitswapProvs[providerResult.Provider.ID]++
				}
			}
		}
	}

	tpl, err := template.New("deal.gohtml").Funcs(map[string]interface{}{
		"EpochTime": func(e abi.ChainEpoch) string {
			return cliutil.EpochTime(now.Height(), e)
		},
		"SizeStr": func(s abi.PaddedPieceSize) string {
			return types.SizeStr(types.NewInt(uint64(s)))
		},
	}).ParseFS(dres, "dexpl/deal.gohtml")
	if err != nil {
		fmt.Println(err)
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html")
	w.WriteHeader(http.StatusOK)
	data := map[string]interface{}{
		"deal":  d,
		"label": lstr,
		"id":    did,

		"provsBitswap": len(bitswapProvs),
		"provsFil":     len(filProvs),

		"contentDesc": cdesc,
	}
	if err := tpl.Execute(w, data); err != nil {
		fmt.Println(err)
		return
	}
}

func (h *dxhnd) handleChain(w http.ResponseWriter, r *http.Request) {
	ts, err := h.api.ChainHead(r.Context())
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	if r.FormValue("epoch") != "" {
		ep, err := strconv.ParseInt(r.FormValue("epoch"), 0, 64)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		ts, err = h.api.ChainGetTipSetByHeight(r.Context(), abi.ChainEpoch(ep), ts.Key())
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
	}

	actors := map[string]cid.Cid{}
	getActor := func(addr address.Address) {
		act, err := h.api.StateGetActor(r.Context(), addr, ts.Key())
		if err != nil {
			return
		}

		var data bytes.Buffer
		if err := act.MarshalCBOR(&data); err != nil {
			return
		}

		c, err := (&cid.V1Builder{Codec: cid.DagCBOR, MhType: multihash.IDENTITY}).Sum(data.Bytes())
		if err != nil {
			return
		}

		actors[addr.String()] = c
	}

	getActor(builtin.SystemActorAddr)
	getActor(builtin.InitActorAddr)
	getActor(builtin.RewardActorAddr)
	getActor(builtin.CronActorAddr)
	getActor(builtin.StoragePowerActorAddr)
	getActor(builtin.StorageMarketActorAddr)
	getActor(builtin.VerifiedRegistryActorAddr)
	getActor(builtin.DatacapActorAddr)
	getActor(builtin.BurntFundsActorAddr)

	tpl, err := template.New("chain.gohtml").ParseFS(dres, "dexpl/chain.gohtml")
	if err != nil {
		fmt.Println(err)
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "text/html")
	w.WriteHeader(http.StatusOK)
	data := map[string]interface{}{
		"ts":     ts,
		"isHead": r.FormValue("epoch") == "",
		"actors": actors,
	}
	if err := tpl.Execute(w, data); err != nil {
		fmt.Println(err)
		return
	}
}

func (h *dxhnd) handleChainActor(w http.ResponseWriter, r *http.Request) {
	ts, err := h.api.ChainHead(r.Context())
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	if r.FormValue("epoch") != "" {
		ep, err := strconv.ParseInt(r.FormValue("epoch"), 0, 64)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		ts, err = h.api.ChainGetTipSetByHeight(r.Context(), abi.ChainEpoch(ep), ts.Key())
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
	}

	addr, err := address.NewFromString(r.FormValue("addr"))
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	act, err := h.api.StateGetActor(r.Context(), addr, ts.Key())
	if err != nil {
		return
	}

	var data bytes.Buffer
	if err := act.MarshalCBOR(&data); err != nil {
		return
	}

	c, err := (&cid.V1Builder{Codec: cid.DagCBOR, MhType: multihash.IDENTITY}).Sum(data.Bytes())
	if err != nil {
		return
	}

	http.Redirect(w, r, "/view/ipfs/"+c.String()+"/?view=ipld", http.StatusFound)
}

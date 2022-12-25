package main

import (
	"encoding/json"
	"fmt"
	"github.com/filecoin-project/go-address"
	"github.com/filecoin-project/go-state-types/abi"
	market9 "github.com/filecoin-project/go-state-types/builtin/v9/market"
	lapi "github.com/filecoin-project/lotus/api"
	"github.com/filecoin-project/lotus/blockstore"
	"github.com/filecoin-project/lotus/chain/actors/adt"
	"github.com/filecoin-project/lotus/chain/actors/builtin/market"
	"github.com/filecoin-project/lotus/chain/types"
	cliutil "github.com/filecoin-project/lotus/cli/util"
	cbor "github.com/ipfs/go-ipld-cbor"
	"github.com/urfave/cli/v2"
	"golang.org/x/xerrors"
	"os"
	"path/filepath"
	"sort"
)

var computeClientMetaCmd = &cli.Command{
	Name:  "client-meta",
	Usage: "Compute market client meta",
	Flags: []cli.Flag{},
	Action: func(cctx *cli.Context) error {
		out := cctx.Args().First()
		if err := os.Mkdir(out, 0755); err != nil {
			return xerrors.Errorf("make out dir: %w", err)
		}

		api, closer, err := cliutil.GetFullNodeAPIV1(cctx)
		if err != nil {
			return err
		}
		defer closer()

		ctx := cliutil.ReqContext(cctx)

		fmt.Println("get list")

		mact, err := api.StateGetActor(ctx, market.Address, types.EmptyTSK)
		if err != nil {
			return err
		}

		stor := adt.WrapStore(ctx, cbor.NewCborStore(blockstore.NewAPIBlockstore(api)))

		mas, err := market.Load(stor, mact)
		if err != nil {
			return err
		}

		ps, err := mas.Proposals()
		if err != nil {
			return err
		}

		var at int64

		deals := map[abi.DealID]*lapi.MarketDeal{}
		err = ps.ForEach(func(id abi.DealID, dp market9.DealProposal) error {
			at++
			if at%4000 == 0 {
				fmt.Printf("\r%d", at)
			}
			deals[id] = &lapi.MarketDeal{Proposal: dp}
			return nil
		})
		if err != nil {
			return err
		}

		fmt.Println("\rprocessing clients")

		{
			type dealMeta struct {
				Count int64
				Data  abi.PaddedPieceSize
			}

			clients := map[address.Address]dealMeta{}

			for _, deal := range deals {
				clients[deal.Proposal.Client] = dealMeta{
					Count: clients[deal.Proposal.Client].Count + 1,
					Data:  clients[deal.Proposal.Client].Data + deal.Proposal.PieceSize,
				}
			}

			type clEntry struct {
				Addr  address.Address
				Count int64
				Data  string
			}

			clEnts := make([]clEntry, 0, len(clients))
			for a, meta := range clients {
				clEnts = append(clEnts, clEntry{
					Addr:  a,
					Count: meta.Count,
					Data:  types.SizeStr(types.NewInt(uint64(meta.Data))),
				})
			}
			sort.Slice(clEnts, func(i, j int) bool {
				return clEnts[i].Count > clEnts[j].Count
			})

			cl, err := os.Create(filepath.Join(out, "clients.json"))
			if err != nil {
				return xerrors.Errorf("create out file: %w", err)
			}
			if err := json.NewEncoder(cl).Encode(clEnts); err != nil {
				return xerrors.Errorf("marshal: %w", err)
			}
			if err := cl.Close(); err != nil {
				return err
			}
		}

		type provMeta struct {
			Prov address.Address
			Data string
			Deal abi.DealID
		}

		fmt.Println("processing deal/miner pairs")

		clDeals := map[address.Address][]provMeta{}

		for d, deal := range deals {
			clDeals[deal.Proposal.Client] = append(clDeals[deal.Proposal.Client], provMeta{
				Prov: deal.Proposal.Provider,
				Data: types.SizeStr(types.NewInt(uint64(deal.Proposal.PieceSize))),
				Deal: abi.DealID(d),
			})
		}

		fmt.Println("writing client metas")

		for a, cds := range clDeals {
			sort.Slice(cds, func(i, j int) bool {
				if cds[i].Prov.String() == cds[j].Prov.String() {
					return cds[i].Deal > cds[j].Deal
				}
				return cds[i].Prov.String() < cds[j].Prov.String()
			})

			cl, err := os.Create(filepath.Join(out, fmt.Sprintf("cl-%s.json", a)))
			if err != nil {
				return xerrors.Errorf("create miner out file: %w", err)
			}
			if err := json.NewEncoder(cl).Encode(cds); err != nil {
				return xerrors.Errorf("marshal: %w", err)
			}
			if err := cl.Close(); err != nil {
				return err
			}
		}

		return nil
	},
}

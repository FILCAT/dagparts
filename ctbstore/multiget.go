package ctbstore

import (
	"context"
	bstore "github.com/filecoin-project/lotus/blockstore"
	"github.com/filecoin-project/lotus/lib/result"
	"github.com/hashicorp/go-multierror"
	blocks "github.com/ipfs/go-block-format"
	"github.com/ipfs/go-cid"
)

type PargetBs struct {
	Backing []bstore.Blockstore
}

func (p *PargetBs) DeleteBlock(ctx context.Context, cid cid.Cid) error {
	//TODO implement me
	panic("implement me")
}

func getFirst[R any](ctx context.Context, bss []bstore.Blockstore, m func(bstore.Blockstore, context.Context, cid.Cid) (R, error), c cid.Cid) (R, error) {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	res := make(chan result.Result[R], len(bss))
	todo := int32(len(bss))

	for _, blockstore := range bss {
		go func(blockstore bstore.Blockstore) {
			res <- result.Wrap(m(blockstore, ctx, c))
		}(blockstore)
	}

	var err error

	for r := range res {
		todo--
		if todo == 0 {
			close(res)
		}

		if r.Error == nil {
			return r.Value, nil
		}
		err = multierror.Append(err, r.Error)
	}

	return *new(R), err
}

func (p *PargetBs) Has(ctx context.Context, cid cid.Cid) (bool, error) {
	return getFirst[bool](ctx, p.Backing, bstore.Blockstore.Has, cid)
}

func (p *PargetBs) Get(ctx context.Context, cid cid.Cid) (blocks.Block, error) {
	return getFirst[blocks.Block](ctx, p.Backing, bstore.Blockstore.Get, cid)
}

func (p *PargetBs) GetSize(ctx context.Context, cid cid.Cid) (int, error) {
	return getFirst[int](ctx, p.Backing, bstore.Blockstore.GetSize, cid)
}

func (p *PargetBs) Put(ctx context.Context, block blocks.Block) error {
	//TODO implement me
	panic("implement me")
}

func (p *PargetBs) PutMany(ctx context.Context, blocks []blocks.Block) error {
	//TODO implement me
	panic("implement me")
}

func (p *PargetBs) AllKeysChan(ctx context.Context) (<-chan cid.Cid, error) {
	//TODO implement me
	panic("implement me")
}

func (p *PargetBs) HashOnRead(enabled bool) {
	//TODO implement me
	panic("implement me")
}

func (p *PargetBs) View(ctx context.Context, cid cid.Cid, callback func([]byte) error) error {
	// todo make this actually view
	v, err := p.Get(ctx, cid)
	if err != nil {
		return err
	}
	return callback(v.RawData())
}

func (p *PargetBs) DeleteMany(ctx context.Context, cids []cid.Cid) error {
	//TODO implement me
	panic("implement me")
}

var _ bstore.Blockstore = &PargetBs{}

package ctbstore

import (
	"context"
	"github.com/filecoin-project/lotus/blockstore"
	blocks "github.com/ipfs/go-block-format"
	"github.com/ipfs/go-cid"
)

type CtxWrap struct {
	sub      blockstore.Blockstore
	wrapFunc func(ctx context.Context) context.Context
}

func (c *CtxWrap) Flush(ctx context.Context) error {
	return c.sub.Flush(c.wrapFunc(ctx))
}

func NewCtxWrap(sub blockstore.Blockstore, wf func(ctx context.Context) context.Context) *CtxWrap {
	return &CtxWrap{sub: sub, wrapFunc: wf}
}

func (c *CtxWrap) Has(ctx context.Context, cid cid.Cid) (bool, error) {
	return c.sub.Has(c.wrapFunc(ctx), cid)
}

func (c *CtxWrap) Get(ctx context.Context, cid cid.Cid) (blocks.Block, error) {
	return c.sub.Get(c.wrapFunc(ctx), cid)
}

func (c *CtxWrap) GetSize(ctx context.Context, cid cid.Cid) (int, error) {
	return c.sub.GetSize(c.wrapFunc(ctx), cid)
}

func (c *CtxWrap) Put(ctx context.Context, block blocks.Block) error {
	return c.sub.Put(c.wrapFunc(ctx), block)
}

func (c *CtxWrap) PutMany(ctx context.Context, blocks []blocks.Block) error {
	return c.sub.PutMany(c.wrapFunc(ctx), blocks)
}

func (c *CtxWrap) AllKeysChan(ctx context.Context) (<-chan cid.Cid, error) {
	return c.sub.AllKeysChan(c.wrapFunc(ctx))
}

func (c *CtxWrap) HashOnRead(enabled bool) {
	c.sub.HashOnRead(enabled)
}

func (c *CtxWrap) View(ctx context.Context, cid cid.Cid, callback func([]byte) error) error {
	return c.sub.View(c.wrapFunc(ctx), cid, callback)
}

func (c *CtxWrap) DeleteBlock(ctx context.Context, cid cid.Cid) error {
	return c.sub.DeleteBlock(c.wrapFunc(ctx), cid)
}

func (c *CtxWrap) DeleteMany(ctx context.Context, cids []cid.Cid) error {
	return c.sub.DeleteMany(c.wrapFunc(ctx), cids)
}

var _ blockstore.Blockstore = &CtxWrap{}

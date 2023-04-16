package ctbstore

import (
	"context"
	"fmt"
	blocks "github.com/ipfs/go-block-format"
	"github.com/ipfs/go-cid"
	"github.com/ipfs/go-datastore"
	"github.com/ipfs/go-datastore/mount"
	"github.com/ipld/go-ipld-prime"
	"github.com/ipld/go-ipld-prime/linking"
	"io"
	"os"

	"golang.org/x/xerrors"

	bstore "github.com/filecoin-project/lotus/blockstore"
	flatfs "github.com/ipfs/go-ds-flatfs"
	blockstore "github.com/ipfs/go-ipfs-blockstore"
	ipldstorage "github.com/ipld/go-ipld-prime/storage"
)

type TempBsb struct {
	root string
}

func NewTempBsBuilder(root string) *TempBsb {
	return &TempBsb{root: root}
}

type TempBs struct {
	bstore.Blockstore

	ffs *flatfs.Datastore

	root string
}

type WhyDoesThisNeedToExistBS struct {
	TBS blockstore.Blockstore
}

func (b *WhyDoesThisNeedToExistBS) Get(ctx context.Context, key string) ([]byte, error) {
	keyCid, err := cid.Cast([]byte(key))
	if err != nil {
		return nil, fmt.Errorf("bad CID key: %w", err)
	}

	bk, err := b.TBS.Get(ctx, keyCid)
	if err != nil {
		return nil, err
	}

	return bk.RawData(), nil
}

func (b *WhyDoesThisNeedToExistBS) Has(ctx context.Context, key string) (bool, error) {
	keyCid, err := cid.Cast([]byte(key))
	if err != nil {
		return false, fmt.Errorf("bad CID key: %w", err)
	}

	return b.TBS.Has(ctx, keyCid)
}

func (b *WhyDoesThisNeedToExistBS) Put(ctx context.Context, key string, content []byte) error {
	keyCid, err := cid.Cast([]byte(key))
	if err != nil {
		return fmt.Errorf("bad CID key: %w", err)
	}

	bk, err := blocks.NewBlockWithCid(content, keyCid)
	if err != nil {
		return fmt.Errorf("bad block: %w", err)
	}

	return b.TBS.Put(ctx, bk)
}

// BlockWriteOpener returns a BlockWriteOpener that operates on this storage.
func (b *WhyDoesThisNeedToExistBS) BlockWriteOpener() linking.BlockWriteOpener {
	return func(lctx linking.LinkContext) (io.Writer, linking.BlockWriteCommitter, error) {
		wr, wrcommit, err := ipldstorage.PutStream(lctx.Ctx, b)
		return wr, func(lnk ipld.Link) error {
			return wrcommit(lnk.Binary())
		}, err
	}
}

var _ ipldstorage.WritableStorage = (*WhyDoesThisNeedToExistBS)(nil)
var _ ipldstorage.ReadableStorage = (*WhyDoesThisNeedToExistBS)(nil)

func (b *TempBsb) MakeStore() (*TempBs, error) {
	bsRoot, err := os.MkdirTemp(b.root, "bs")
	if err != nil {
		return nil, err
	}

	fsbs, err := flatfs.CreateOrOpen(bsRoot, flatfs.NextToLast(2), false)
	if err != nil {
		return nil, err
	}

	mbs := mount.New([]mount.Mount{
		{
			Prefix:    datastore.NewKey("/blocks"),
			Datastore: fsbs,
		},
	})

	bs := blockstore.NewBlockstore(mbs)

	return &TempBs{
		Blockstore: bstore.Adapt(bs),
		ffs:        fsbs,
		root:       bsRoot,
	}, nil
}

func (b *TempBs) Release() error {
	if err := b.ffs.Close(); err != nil {
		return xerrors.Errorf("close temp store flatfs: %w", err)
	}

	if err := os.RemoveAll(b.root); err != nil {
		return xerrors.Errorf("remove temp store: %w", err)
	}

	return nil
}

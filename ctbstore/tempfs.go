package ctbstore

import (
	"github.com/ipfs/go-datastore"
	"github.com/ipfs/go-datastore/mount"
	"os"

	"golang.org/x/xerrors"

	bstore "github.com/filecoin-project/lotus/blockstore"
	flatfs "github.com/ipfs/go-ds-flatfs"
	blockstore "github.com/ipfs/go-ipfs-blockstore"
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

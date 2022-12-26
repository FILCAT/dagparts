package main

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestDecodeSegment(t *testing.T) {
	for _, tc := range []struct {
		Seg string

		Result string
		Unix   bool

		Err string
	}{
		{
			Seg:    "some",
			Result: "some",
			Unix:   true,
		},
		{
			Seg:    "~some",
			Result: "some",
			Unix:   false,
		},
		{
			Seg:    "~~~upath",
			Result: "~upath",
			Unix:   true,
		},
		{
			Seg:    "~~ipath",
			Result: "~path",
			Unix:   false,
		},
		{
			Seg: "~~wat",
			Err: "unknown segment mode 'w'",
		},
	} {
		rs, ru, err := decodeSegment(tc.Seg)
		if tc.Err != "" {
			require.Error(t, err)
			require.Equal(t, tc.Err, err.Error())
			continue
		}

		require.NoError(t, err)
		require.Equal(t, tc.Result, rs)
		require.Equal(t, tc.Unix, ru)
	}
}

package chainreader

import (
	"testing"

	"github.com/smartcontractkit/chainlink-common/pkg/logger"
	"github.com/smartcontractkit/chainlink-common/pkg/utils/tests"
	"github.com/stretchr/testify/require"
)

func Test_PDAIndexer(t *testing.T) {
	ctx := tests.Context(t)
	lggr := logger.Test(t)

	p := NewPDAIndexer(lggr)
	require.NoError(t, p.Start(ctx))
	require.NoError(t, p.Close())
}

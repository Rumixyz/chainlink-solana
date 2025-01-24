package chainreader

import (
	"github.com/smartcontractkit/chainlink-common/pkg/logger"
	"github.com/smartcontractkit/chainlink-common/pkg/services"
)

type pdaIndexer struct {
	services.Service
	lggr logger.Logger
}

func NewPDAIndexer(lggr logger.Logger) *pdaIndexer {
	svc, _ := services.Config{
		Name: "PDAIndexer",
	}.NewServiceEngine(lggr)
	return &pdaIndexer{
		Service: svc,
	}
}

package chainreader

import (
	"context"
	"time"

	"github.com/gagliardetto/solana-go"
	"github.com/smartcontractkit/chainlink-common/pkg/logger"
	"github.com/smartcontractkit/chainlink-common/pkg/services"

	"github.com/smartcontractkit/chainlink-solana/pkg/solana/client"
)

var _ PDAIndexer = &pdaIndexer{}

type pdaIndexer struct {
	services.Service
	*services.Engine
	client   client.MultiClient
	lggr     logger.Logger
	matchers []pdaMatcher
}

type pdaMatcher struct {
	owner  solana.PublicKey
	offset uint64
	bytes  []byte
}

type pdaIndexerConfig struct {
	matchers []pdaMatcher
}

func NewPDAIndexer(cl client.MultiClient, lggr logger.Logger, cfg pdaIndexerConfig) *pdaIndexer {
	lggr = logger.Sugared(logger.Named(lggr, "LogPoller"))
	p := &pdaIndexer{
		client:   cl,
		matchers: cfg.matchers,
	}
	if p.matchers == nil {
		p.matchers = make([]pdaMatcher, 0)
	}

	p.Service, p.Engine = services.Config{
		Name:  "PDAIndexer",
		Start: p.start,
	}.NewServiceEngine(lggr)
	return p
}

func (p *pdaIndexer) start(_ context.Context) error {
	p.GoTick(services.TickerConfig{}.NewTicker(time.Second), p.poll)
	return nil
}

func (p *pdaIndexer) poll(ctx context.Context) {
	for _, matcher := range p.matchers {
		p.client.GetProgramAccountsBySeed(ctx, matcher.owner, matcher.offset, matcher.bytes)
	}
}

func (p *pdaIndexer) GetAccount(addr solana.PublicKey, seed solana.PublicKey, offset uint64) {

}

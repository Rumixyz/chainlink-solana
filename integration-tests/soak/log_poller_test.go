package soak

import (
	"os"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/smartcontractkit/chainlink-solana/integration-tests/common"
	tc "github.com/smartcontractkit/chainlink-solana/integration-tests/testconfig"
	"github.com/smartcontractkit/chainlink-solana/integration-tests/utils"
)

func TestLogPollerPerformance(t *testing.T) {
	config, err := tc.GetConfig("Smoke", tc.OCR2)
	if err != nil {
		t.Fatal(err)
	}
	config.EnvVariables = map[string]interface{}{
		"LOG_POLLER_TEST":         os.Getenv("LOG_POLLER_TEST"), // MIMIC|MOCKER_RATE
		"SOURCE_CONTRACT_ADDRESS": "",                           // only required for MIMIC test
		"EVENTS_PER_SEC":          "10",                         // only used by MOCKED_RATE
	}
	name := "lp-mimic-performance"
	state, err := common.NewOCRv2State(t, 1, name, &config)
	require.NoError(t, err, "Could not setup the ocrv2 state")
	state.DeployCluster(utils.ContractsDir)
}

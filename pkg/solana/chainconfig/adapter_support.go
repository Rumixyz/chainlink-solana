package chainconfig

import (
	"github.com/smartcontractkit/chainlink-solana/pkg/solana/client"
)

type SolanaAdapterSupportImpl struct {
	SolanaAdapterSupport
	reader client.Reader
}

//func (sasi SolanaAdapterSupportImpl) GetPDAAddresses(program solana.PublicKey, seeds []chainwriter.Seed) (solana.PublicKeySlice, error) {
//
//}

// Gets the data from a data account in solana and writes the content to into
//func (sasi SolanaAdapterSupportImpl) GetDataAccount(address solana.PublicKey, into any) error {
//	accountInfoResult, err := sasi.reader.GetAccountInfoWithOpts(nil, address, &rpc.GetAccountInfoOpts{
//		Encoding:   "base64",
//		Commitment: rpc.CommitmentFinalized,
//	})
//	if err != nil {
//		return err
//	}
//
//	accountInfoResult.GetBinary()
//}

//// Generic conversion function from string to solana key
//func (sasi SolanaAdapterSupportImpl) ToPublicKey(address string) solana.PublicKey {
//
//}
//
//// Same as get GetDataAccount but with a fix expected output type for lookup tables content
//func (sasi SolanaAdapterSupportImpl) GetLookupTableData(key solana.PublicKey) solana.PublicKeySlice {
//
//}

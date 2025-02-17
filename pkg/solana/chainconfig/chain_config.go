package chainconfig

import (
	"github.com/gagliardetto/solana-go"
	"github.com/smartcontractkit/chainlink-common/pkg/types/query"
	"github.com/smartcontractkit/chainlink-common/pkg/types/query/primitives"
	"github.com/smartcontractkit/chainlink-solana/pkg/solana/chainwriter"
)

// ==== ADAPTER SUPPORT API ====
// Solana chain-specific API avaialble for the adapter implementation to access Solana chain functionality.
// This API should have restrictions around the set of operations that can be done against the chain. For instance there shouldn't be support for submitting transactions.
// The functionality provided here should be everything it would be needed to accomplish any functionality required by a product.
// For instance, users of this API can fetch any data account from Solana so if we store static data or dynamic data directly in solana that's required
// in order to get all the account addresses needed to execute a transaction or to find all the lookup tables to minimize transaction. We woulnd't be imposing any limitations on how the user
// wants to store the data, the have the ability to do whatever they want.
// TODO consider Matt's example for gas estimation.
type SolanaAdapterSupport interface {

	// Calculates the address of a set of program derived accounts
	GetPDAAddresses(program solana.PublicKey, seeds []chainwriter.Seed) (solana.PublicKeySlice, error)

	// Gets the data from a data account in solana and writes the content to into
	GetDataAccount(key solana.PublicKey, into any)

	// Generic conversion function from string to solana key
	ToPublicKey(address string) solana.PublicKey

	// Same as get GetDataAccount but with a fix expected output type for lookup tables content
	GetLookupTableData(key solana.PublicKey) solana.PublicKeySlice
}

// ==== CONTEXT API =====
// Context API provided to the Adapter functions

// BaseReadContext, ReadLatestContext, QueryKeyContext, Write context should be moved to chain abstraction layer in chainlink-common since it would be the same for all chains
type BaseReadContext struct {
	ReadIdentifier  string
	ConfidenceLevel primitives.ConfidenceLevel
}

type ReadLatestContext struct {
	BaseReadContext
	InputArgs any
}

type QueryKeyContext struct {
	BaseReadContext
	Filter       query.KeyFilter
	LimitAndSort query.LimitAndSort
}

type WriteContext struct {
	Contract  string
	Method    string
	InputArgs any
	ToAddress string
}

// Solana specific context API. It may provide access to additional data like the config defined by the user for the specific read entity / write operation
// Context for Read Latest operation adapters
type SolanaReadLatestContext struct {
	ReadContext ReadLatestContext
	//Solana specific data. This may come out of the Solana Config for this read entity.
	SomeField any
}

// Context for Submit Transaction operation adapters.
type SolanaWriteContext struct {
	WriteContext WriteContext
	//Solana specific data. This may come out of the Solana Config for this read entity.
	SomeData any
}

// ==== ADAPTER API =====
// Adapter API that can be configured in the SolanaConfig

// Adapter function to be invoked when there's a ChainReader.GetLatestValue(..) invocation
type ReadLatestAdapterFunc func(input any, adapterSupport SolanaAdapterSupport, context SolanaReadLatestContext) any

type WriteAdapterOutput struct {
	//The updated (or same) data as the SubmitTransaction(..) input param based on the on-chain type.
	Data any
	//Per lookup table give the accounts being used
	LookupTables map[solana.PublicKey]solana.PublicKeySlice
	//List of accounts used during the transaction
	AccountsMeta []solana.AccountMeta
}

// Adater function to be invoked when there's a ChainWriter.SubmitTranscation(..) invocation
type WriteAdapterFunc func(input any, adapterSupport SolanaAdapterSupport, context WriteContext) (WriteAdapterOutput, error)

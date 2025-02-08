package chainreader

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"

	"github.com/gagliardetto/solana-go"

	"github.com/smartcontractkit/chainlink-common/pkg/types"
	"github.com/smartcontractkit/chainlink-common/pkg/values"
	"github.com/smartcontractkit/chainlink-solana/pkg/solana/config"
)

type call struct {
	Namespace, ReadName string
	Params, ReturnVal   any
}

type batchResultWithErr struct {
	address             string
	namespace, readName string
	returnVal           any
	err                 error
}

var (
	ErrMissingAccountData = errors.New("account data not found")
)

type MultipleAccountGetter interface {
	GetMultipleAccountData(context.Context, ...solana.PublicKey) ([][]byte, error)
}

// doMultiRead aggregate results from multiple PDAs from the same contract into one result.
func doMultiRead(ctx context.Context, client MultipleAccountGetter, bindings bindingsRegistry, rv readValues, returnValue any) error {
	batch := make([]call, len(rv.multiRead))
	for idx, readName := range rv.multiRead {
		batch[idx] = call{
			Namespace: rv.contract,
			ReadName:  readName,
			ReturnVal: returnValue,
		}
	}

	results, err := doMethodBatchCall(ctx, client, bindings, batch)
	if err != nil {
		return err
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("failed to do a multiRead: %q on contract: %q with address: %q with: %d total calls:\n", rv.multiRead[0], rv.contract, rv.address, len(rv.multiRead)))

	var errCount int
	for i, r := range results {
		if r.err != nil {
			errCount++
			sb.WriteString(fmt.Sprintf("- call: #%d with readName: %q and address: %q failed with err: %s\n", i+1, r.readName, r.address, r.err))
		}
	}

	if errCount != 0 {
		return errors.New(sb.String())
	}

	return nil
}

type resultIndex struct {
	contractName, readName string
	readType               config.ReadType
}

func doMethodBatchCall(ctx context.Context, client MultipleAccountGetter, bindingsRegistry bindingsRegistry, batch []call) ([]batchResultWithErr, error) {
	resultIndexes := make(map[int]resultIndex)
	var regularBatch []call
	var splitParamsBatch []call
	// Create the list of public keys to fetch
	regularKeys := make([]solana.PublicKey, len(batch))
	for idx, batchCall := range batch {
		rBinding, err := bindingsRegistry.GetReadBinding(batchCall.Namespace, batchCall.ReadName)
		if err != nil {
			return nil, fmt.Errorf("%w: read binding not found for contract: %q read: %q: %w", types.ErrInvalidConfig, batchCall.Namespace, batchCall.ReadName, err)
		}

		resultIndexes[idx] = resultIndex{
			contractName: batchCall.Namespace,
			readName:     batchCall.ReadName,
			readType:     rBinding.ReadType()}

		if rBinding.ReadType() == config.AccountSplitParams {
			splitParamsBatch = append(splitParamsBatch, batchCall)
		} else {
			regularKeys[idx], err = rBinding.GetAddress(ctx, batchCall.Params)
			if err != nil {
				return nil, fmt.Errorf("failed to get address for contract: %q read: %q: %w", batchCall.Namespace, batchCall.ReadName, err)
			}
			regularBatch = append(regularBatch, batchCall)
		}
	}

	var splitParamBatchCallsResults []batchResultWithErr
	for _, callToSplit := range splitParamsBatch {
		rBinding, err := bindingsRegistry.GetReadBinding(callToSplit.Namespace, callToSplit.ReadName)
		if err != nil {
			return nil, fmt.Errorf("%w: read binding not found for contract: %q read: %q: %w", types.ErrInvalidConfig, callToSplit.Namespace, callToSplit.ReadName, err)
		}

		results, err := doSplitParamsBatchCall(ctx, client, callToSplit, bindingsRegistry, rBinding)
		if err != nil {
			return nil, err
		}

		splitParamBatchCallsResults = append(splitParamBatchCallsResults, results)
	}

	// Fetch the account data
	data, err := client.GetMultipleAccountData(ctx, regularKeys...)
	if err != nil {
		return nil, err
	}

	return mergeBatchResults(decodeBatchResults(ctx, batch, regularKeys, data, bindingsRegistry), splitParamBatchCallsResults, resultIndexes)
}

func mergeBatchResults(regularResults, splitParamBatchCallsResults []batchResultWithErr, resultIndexes map[int]resultIndex) ([]batchResultWithErr, error) {
	finalResults := make([]batchResultWithErr, len(resultIndexes))
	seen := make(map[int]bool)

	for _, result := range regularResults {
		for key, resIndex := range resultIndexes {
			if result.namespace == resIndex.contractName && result.readName == resIndex.readName && resIndex.readType != config.AccountSplitParams {
				if !seen[key] {
					finalResults[key] = result
					seen[key] = true
				}
			}
		}
	}

	for _, result := range splitParamBatchCallsResults {
		for key, resIndex := range resultIndexes {
			if result.namespace == resIndex.contractName && result.readName == resIndex.readName && resIndex.readType == config.AccountSplitParams {
				if !seen[key] {
					finalResults[key] = result
					seen[key] = true
				}
			}
		}
	}

	var mergeResErr error
	for key, val := range seen {
		if !val {
			mergeResErr = errors.Join(mergeResErr, fmt.Errorf("failed to find result for call: %v", resultIndexes[key]))
		}
	}

	if mergeResErr != nil {
		return nil, mergeResErr
	}

	if len(finalResults) != len(resultIndexes) {
		return nil, fmt.Errorf("%w failed to mere batch results, final results length does not match batch length", types.ErrInternal)
	}

	return finalResults, nil
}

func doSplitParamsBatchCall(ctx context.Context, client MultipleAccountGetter, callToSplit call, bindings bindingsRegistry, binding readBinding) (batchResultWithErr, error) {
	sPBatch, err := getSplitParamsBatch(callToSplit)
	if err != nil {
		return batchResultWithErr{}, err
	}

	var sPKeys []solana.PublicKey
	for _, spCall := range sPBatch {
		key, err := binding.GetAddress(ctx, spCall.Params)
		if err != nil {
			return batchResultWithErr{}, fmt.Errorf("failed to get address for contract: %q read: %q: %w", callToSplit.Namespace, callToSplit.ReadName, err)
		}
		sPKeys = append(sPKeys, key)
	}

	data, err := client.GetMultipleAccountData(ctx, sPKeys...)
	if err != nil {
		return batchResultWithErr{}, err
	}

	results := decodeBatchResults(ctx, sPBatch, sPKeys, data, bindings)

	if len(results) != len(sPBatch) {
		return batchResultWithErr{}, fmt.Errorf("results length does not match split params batch length for contract: %q read: %q", callToSplit.Namespace, callToSplit.ReadName)
	}

	var returnVal []any
	var returnErr error
	for _, res := range results {
		returnVal = append(returnVal, res.returnVal)
		returnErr = errors.Join(returnErr, res.err)
	}

	return batchResultWithErr{
		namespace: callToSplit.Namespace,
		readName:  callToSplit.ReadName,
		returnVal: returnVal,
		err:       returnErr,
	}, nil
}

func decodeBatchResults(ctx context.Context, batch []call, keys []solana.PublicKey, data [][]byte, bindings bindingsRegistry) []batchResultWithErr {
	results := make([]batchResultWithErr, len(batch))

	// decode batch call results
	for idx, batchCall := range batch {
		results[idx] = batchResultWithErr{
			address:   keys[idx].String(),
			namespace: batchCall.Namespace,
			readName:  batchCall.ReadName,
			returnVal: batchCall.ReturnVal,
		}

		if data[idx] == nil || len(data[idx]) == 0 {
			results[idx].err = ErrMissingAccountData

			continue
		}

		rBinding, err := bindings.GetReadBinding(results[idx].namespace, results[idx].readName)
		if err != nil {
			results[idx].err = err

			continue
		}

		results[idx].err = errors.Join(
			decodeReturnVal(ctx, rBinding, data[idx], results[idx].returnVal),
			results[idx].err)
	}
	return results
}

// decodeReturnVal checks if returnVal is a *values.Value vs. a normal struct pointer, and decodes accordingly.
func decodeReturnVal(ctx context.Context, binding readBinding, raw []byte, returnVal any) error {
	// If we are not dealing with a `*values.Value`, just decode directly.
	ptrToValue, isValue := returnVal.(*values.Value)
	if !isValue {
		return binding.Decode(ctx, raw, returnVal)
	}

	// Otherwise, we need to create an intermediate type, decode into it,
	// wrap it, and set it back into *values.Value
	contractType, err := binding.CreateType(false)
	if err != nil {
		return err
	}

	if err = binding.Decode(ctx, raw, contractType); err != nil {
		return err
	}

	value, err := values.Wrap(contractType)
	if err != nil {
		return err
	}

	*ptrToValue = value

	return nil
}

func getSplitParamsBatch(c call) ([]call, error) {
	sParams, isOk := extractSliceElements(c.Params)
	if !isOk {
		return nil, fmt.Errorf("failed to extract params slice elements for contract: %q split params read: %q", c.Namespace, c.ReadName)
	}

	sReturnVals, isOk := extractSliceElements(c.ReturnVal)
	if !isOk {
		return nil, fmt.Errorf("failed to extract return values slice elements for contract: %q split params read: %q", c.Namespace, c.ReadName)
	}

	if len(sParams) != len(sReturnVals) {
		return nil, fmt.Errorf("params and return values slice lengths do not match for contract: %q split params read: %q", c.Namespace, c.ReadName)
	}

	batch := make([]call, len(sParams))
	for idx := range sParams {
		batch[idx] = call{
			Namespace: c.Namespace,
			ReadName:  c.ReadName,
			Params:    sParams[idx],
			ReturnVal: sReturnVals[idx],
		}
	}

	return batch, nil
}

func extractSliceElements(input any) ([]any, bool) {
	rv := reflect.ValueOf(input)
	if rv.Kind() != reflect.Slice {
		return nil, false
	}

	length := rv.Len()
	elements := make([]any, length)
	for i := 0; i < length; i++ {
		elements[i] = rv.Index(i).Interface()
	}

	return elements, true
}

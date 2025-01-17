package input

import (
	"context"
	"io"

	"github.com/stellar/stellar-etl/internal/utils"

	"github.com/stellar/go/ingest"
	"github.com/stellar/go/xdr"
)

// LedgerTransformInput is a representation of the input for the TransformTransaction function
type LedgerTransformInput struct {
	Transaction   ingest.LedgerTransaction
	LedgerHistory xdr.LedgerHeaderHistoryEntry
}

// GetTransactions returns a slice of ledger close metas for the ledgers in the provided range (inclusive on both ends)
func GetTransactions(start, end uint32, limit int64, isTest bool) ([]LedgerTransformInput, error) {
	env := utils.GetEnvironmentDetails(isTest)
	backend, err := utils.CreateBackend(start, end, env.ArchiveURLs)
	if err != nil {
		return []LedgerTransformInput{}, err
	}

	var txSlice []LedgerTransformInput
	ctx := context.Background()
	for seq := start; seq <= end; seq++ {
		txReader, err := ingest.NewLedgerTransactionReader(ctx, backend, env.NetworkPassphrase, seq)
		if err != nil {
			return []LedgerTransformInput{}, err
		}

		lhe := txReader.GetHeader()
		// A negative limit value means that all input should be processed
		for int64(len(txSlice)) < limit || limit < 0 {
			tx, err := txReader.Read()
			if err == io.EOF {
				break
			}

			txSlice = append(txSlice, LedgerTransformInput{
				Transaction:   tx,
				LedgerHistory: lhe,
			})
		}

		txReader.Close()
		if int64(len(txSlice)) >= limit && limit >= 0 {
			break
		}
	}

	return txSlice, nil
}

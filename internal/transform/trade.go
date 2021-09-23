package transform

import (
	"fmt"
	"time"

	"github.com/stellar/stellar-etl/internal/toid"
	"github.com/stellar/stellar-etl/internal/utils"

	"github.com/stellar/go/ingest"
	"github.com/stellar/go/xdr"
)

// TransformTrade converts a relevant operation from the history archive ingestion system into a form suitable for BigQuery
func TransformTrade(operationIndex int32, operationID int64, transaction ingest.LedgerTransaction, ledgerCloseTime time.Time) ([]TradeOutput, error) {
	operationResults, ok := transaction.Result.OperationResults()
	if !ok {
		return []TradeOutput{}, fmt.Errorf("Could not get any results from this transaction")
	}

	if !transaction.Result.Successful() {
		return []TradeOutput{}, fmt.Errorf("Transaction failed; no trades")
	}

	operation := transaction.Envelope.Operations()[operationIndex]
	// operation id is +1 incremented to stay in sync with ingest package
	outputOperationID := operationID + 1
	claimedOffers, counterOffer, err := extractClaimedOffers(operationResults, operationIndex, operation.Body.Type)
	if err != nil {
		return []TradeOutput{}, err
	}

	var outputCounterOfferID int64
	if counterOffer != nil {
		outputCounterOfferID = int64(counterOffer.OfferId)
	} else {
		outputCounterOfferID = toid.EncodeOfferId(uint64(operationID), toid.TOIDType)
	}

	transformedTrades := []TradeOutput{}
	sourceAccount := transaction.Envelope.SourceAccount()

	for claimOrder, claimOffer := range claimedOffers {
		outputOrder := int32(claimOrder)
		outputLedgerClosedAt := ledgerCloseTime

		outputOfferID := int64(claimOffer.OfferId)
		if outputOfferID < 0 {
			return []TradeOutput{}, fmt.Errorf("Offer ID is negative (%d) for operation at index %d", outputOfferID, operationIndex)
		}

		outputBaseAccountAddress, err := claimOffer.SellerId.GetAddress()
		if err != nil {
			return []TradeOutput{}, err
		}

		var outputBaseAssetType, outputBaseAssetCode, outputBaseAssetIssuer string
		err = claimOffer.AssetSold.Extract(&outputBaseAssetType, &outputBaseAssetCode, &outputBaseAssetIssuer)
		if err != nil {
			return []TradeOutput{}, err
		}

		outputBaseAmount := int64(claimOffer.AmountSold)
		if outputBaseAmount < 0 {
			return []TradeOutput{}, fmt.Errorf("Amount sold is negative (%d) for operation at index %d", outputBaseAmount, operationIndex)
		}

		outputCounterAccountAddress, err := utils.GetAccountAddressFromMuxedAccount(sourceAccount)
		if err != nil {
			return []TradeOutput{}, err
		}

		var outputCounterAssetType, outputCounterAssetCode, outputCounterAssetIssuer string
		err = claimOffer.AssetBought.Extract(&outputCounterAssetType, &outputCounterAssetCode, &outputCounterAssetIssuer)
		if err != nil {
			return []TradeOutput{}, err
		}

		outputCounterAmount := int64(claimOffer.AmountBought)
		if outputCounterAmount < 0 {
			return []TradeOutput{}, fmt.Errorf("Amount bought is negative (%d) for operation at index %d", outputCounterAmount, operationIndex)
		}

		if outputBaseAmount == 0 && outputCounterAmount == 0 {
			return []TradeOutput{}, fmt.Errorf("Both base and counter amount are 0 for operation at index %d", operationIndex)
		}

		// Final price should be buy / sell
		outputPriceN, outputPriceD := outputCounterAmount, outputBaseAmount

		outputBaseIsSeller := true

		trade := TradeOutput{
			Order:                 outputOrder,
			LedgerClosedAt:        outputLedgerClosedAt,
			OfferID:               outputOfferID,
			BaseAccountAddress:    outputBaseAccountAddress,
			BaseAssetType:         outputBaseAssetType,
			BaseAssetCode:         outputBaseAssetCode,
			BaseAssetIssuer:       outputBaseAssetIssuer,
			BaseAmount:            outputBaseAmount,
			CounterAccountAddress: outputCounterAccountAddress,
			CounterAssetType:      outputCounterAssetType,
			CounterAssetCode:      outputCounterAssetCode,
			CounterAssetIssuer:    outputCounterAssetIssuer,
			CounterAmount:         outputCounterAmount,
			BaseIsSeller:          outputBaseIsSeller,
			PriceN:                outputPriceN,
			PriceD:                outputPriceD,
			BaseOfferID:           outputOfferID,
			CounterOfferID:        outputCounterOfferID,
			HistoryOperationID:    outputOperationID,
		}

		transformedTrades = append(transformedTrades, trade)
	}
	return transformedTrades, nil
}

func extractClaimedOffers(operationResults []xdr.OperationResult, operationIndex int32, operationType xdr.OperationType) (claimedOffers []xdr.ClaimOfferAtom, counterOffer *xdr.OfferEntry, err error) {
	if operationIndex >= int32(len(operationResults)) {
		err = fmt.Errorf("Operation index of %d is out of bounds in result slice (len = %d)", operationIndex, len(operationResults))
		return
	}

	if operationResults[operationIndex].Tr == nil {
		err = fmt.Errorf("Could not get result Tr for operation at index %d", operationIndex)
		return
	}

	operationTr, ok := operationResults[operationIndex].GetTr()
	if !ok {
		err = fmt.Errorf("Could not get result Tr for operation at index %d", operationIndex)
		return
	}

	switch operationType {
	case xdr.OperationTypeManageBuyOffer:
		var buyOfferResult xdr.ManageBuyOfferResult
		if buyOfferResult, ok = operationTr.GetManageBuyOfferResult(); !ok {
			err = fmt.Errorf("Could not get ManageBuyOfferResult for operation at index %d", operationIndex)
			return
		}

		if success, ok := buyOfferResult.GetSuccess(); ok {
			if claimedOffers, err = getClaimedOrderBook(success.OffersClaimed); err != nil {
				err = fmt.Errorf("Could not get orderbook: %v", err)
				return
			}
			counterOffer = success.Offer.Offer
			return
		}

		err = fmt.Errorf("Could not get ManageOfferSuccess for operation at index %d", operationIndex)

	case xdr.OperationTypeManageSellOffer:
		var sellOfferResult xdr.ManageSellOfferResult
		if sellOfferResult, ok = operationTr.GetManageSellOfferResult(); !ok {
			err = fmt.Errorf("Could not get ManageSellOfferResult for operation at index %d", operationIndex)
			return
		}

		if success, ok := sellOfferResult.GetSuccess(); ok {
			if claimedOffers, err = getClaimedOrderBook(success.OffersClaimed); err != nil {
				err = fmt.Errorf("Could not get orderbook: %v", err)
				return
			}
			counterOffer = success.Offer.Offer
			return
		}

		err = fmt.Errorf("Could not get ManageOfferSuccess for operation at index %d", operationIndex)

	case xdr.OperationTypeCreatePassiveSellOffer:
		// KNOWN ISSUE: stellar-core creates results for CreatePassiveOffer operations
		// with the wrong result arm set.
		if operationTr.Type == xdr.OperationTypeManageSellOffer {
			passiveSellResult := operationTr.MustManageSellOfferResult().MustSuccess()
			if claimedOffers, err = getClaimedOrderBook(passiveSellResult.OffersClaimed); err != nil {
				err = fmt.Errorf("Could not get orderbook: %v", err)
				return
			}
			counterOffer = passiveSellResult.Offer.Offer
			return
		} else {
			passiveSellResult := operationTr.MustCreatePassiveSellOfferResult().MustSuccess()
			if claimedOffers, err = getClaimedOrderBook(passiveSellResult.OffersClaimed); err != nil {
				err = fmt.Errorf("Could not get orderbook: %v", err)
				return
			}
			counterOffer = passiveSellResult.Offer.Offer
			return
		}

	case xdr.OperationTypePathPaymentStrictSend:
		var pathSendResult xdr.PathPaymentStrictSendResult
		if pathSendResult, ok = operationTr.GetPathPaymentStrictSendResult(); !ok {
			err = fmt.Errorf("Could not get PathPaymentStrictSendResult for operation at index %d", operationIndex)
			return
		}

		success, ok := pathSendResult.GetSuccess()
		if ok {
			if claimedOffers, err = getClaimedOrderBook(success.Offers); err != nil {
				err = fmt.Errorf("Could not get orderbook: %v", err)
				return
			}
			return
		}

		err = fmt.Errorf("Could not get PathPaymentStrictSendSuccess for operation at index %d", operationIndex)

	case xdr.OperationTypePathPaymentStrictReceive:
		var pathReceiveResult xdr.PathPaymentStrictReceiveResult
		if pathReceiveResult, ok = operationTr.GetPathPaymentStrictReceiveResult(); !ok {
			err = fmt.Errorf("Could not get PathPaymentStrictReceiveResult for operation at index %d", operationIndex)
			return
		}

		if success, ok := pathReceiveResult.GetSuccess(); ok {
			if claimedOffers, err = getClaimedOrderBook(success.Offers); err != nil {
				err = fmt.Errorf("Could not get orderbook: %v", err)
				return
			}
			return
		}

		err = fmt.Errorf("Could not get GetPathPaymentStrictReceiveSuccess for operation at index %d", operationIndex)

	default:
		err = fmt.Errorf("Operation of type %s at index %d does not result in trades", operationType, operationIndex)
		return
	}

	return
}

func getClaimedOrderBook(offers []xdr.ClaimAtom) (orderBookOffers []xdr.ClaimOfferAtom, err error) {
	var singleClaimOfferAtom xdr.ClaimOfferAtom
	for _, singleOffer := range offers {
		switch singleOffer.Type {
		case xdr.ClaimAtomTypeClaimAtomTypeV0:
			// Protocols 17 and 18 changes the Orderbook structure
			singleOfferOrders, ok := singleOffer.GetV0()
			if !ok {
				err = fmt.Errorf("Could not fetch V0 type for xdr.ClaimAtom")
				return orderBookOffers, err
			}
			singleClaimOfferAtom.SellerId.Ed25519 = &singleOfferOrders.SellerEd25519
			singleClaimOfferAtom.SellerId.Type = xdr.PublicKeyTypePublicKeyTypeEd25519
			singleClaimOfferAtom.OfferId = singleOfferOrders.OfferId
			singleClaimOfferAtom.AssetSold = singleOfferOrders.AssetSold
			singleClaimOfferAtom.AmountSold = singleOfferOrders.AmountSold
			singleClaimOfferAtom.AssetBought = singleOfferOrders.AssetBought
			singleClaimOfferAtom.AmountBought = singleOfferOrders.AmountBought
			orderBookOffers = append(orderBookOffers, singleClaimOfferAtom)

		case xdr.ClaimAtomTypeClaimAtomTypeOrderBook:
			singleOfferOrders, ok := singleOffer.GetOrderBook()
			if !ok {
				err = fmt.Errorf("Could not fetch Orderbook type for xdr.ClaimAtom")
				return orderBookOffers, err
			}
			orderBookOffers = append(orderBookOffers, singleOfferOrders)

		default:
			err = fmt.Errorf("Could not parse the ClaimAtomType")
			return
		}
	}

	return orderBookOffers, nil
}

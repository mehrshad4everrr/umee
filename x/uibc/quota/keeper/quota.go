package keeper

import (
	"strings"
	"time"

	sdkmath "cosmossdk.io/math"
	sdk "github.com/cosmos/cosmos-sdk/types"
	transfertypes "github.com/cosmos/ibc-go/v6/modules/apps/transfer/types"
	channeltypes "github.com/cosmos/ibc-go/v6/modules/core/04-channel/types"
	"github.com/cosmos/ibc-go/v6/modules/core/exported"

	"github.com/umee-network/umee/v5/util"
	"github.com/umee-network/umee/v5/util/store"
	ltypes "github.com/umee-network/umee/v5/x/leverage/types"
	"github.com/umee-network/umee/v5/x/uibc"
)

var ten = sdk.MustNewDecFromStr("10")

// GetAllOutflows returns sum of outflows of all tokens in USD value.
func (k Keeper) GetAllOutflows() (sdk.DecCoins, error) {
	var outflows sdk.DecCoins
	// creating PrefixStore upfront will remove the prefix from the key when running the iterator.
	store := k.PrefixStore(keyPrefixDenomOutflows)
	iter := sdk.KVStorePrefixIterator(store, nil)
	defer iter.Close()

	for ; iter.Valid(); iter.Next() {
		o := sdk.DecCoin{Denom: string(iter.Key())}
		if err := o.Amount.Unmarshal(iter.Value()); err != nil {
			return nil, err
		}
		outflows = append(outflows, o)
	}

	return outflows, nil
}

// GetTokenOutflows returns sum of denom outflows in USD value in the DecCoin structure.
func (k Keeper) GetTokenOutflows(denom string) sdk.DecCoin {
	amount := store.GetDec(k.store, KeyTotalOutflows(denom), "total_outflow")
	return sdk.NewDecCoinFromDec(denom, amount)
}

// SetTokenOutflows saves provided updated IBC outflows as a pair: USD value, denom name in the
// DecCoin structure.
func (k Keeper) SetTokenOutflows(outflows sdk.DecCoins) {
	for _, q := range outflows {
		k.SetTokenOutflow(q)
	}
}

// SetTokenOutflow save the outflows of denom into store.
func (k Keeper) SetTokenOutflow(outflow sdk.DecCoin) {
	key := KeyTotalOutflows(outflow.Denom)
	err := store.SetDec(k.store, key, outflow.Amount, "total_outflow")
	util.Panic(err)
}

// GetTotalOutflow returns the total outflow of ibc-transfer amount.
func (k Keeper) GetTotalOutflow() sdk.Dec {
	// TODO: use store.Get/SetDec
	bz := k.store.Get(keyTotalOutflows)
	return sdk.MustNewDecFromStr(string(bz))
}

// SetTotalOutflowSum save the total outflow of ibc-transfer amount.
func (k Keeper) SetTotalOutflowSum(amount sdk.Dec) {
	k.store.Set(keyTotalOutflows, []byte(amount.String()))
}

// SetExpire save the quota expire time of ibc denom into.
func (k Keeper) SetExpire(expires time.Time) error {
	return store.SetBinValue(k.store, keyQuotaExpires, &expires, "expire")
}

// GetExpire returns ibc-transfer quota expires time.
func (k Keeper) GetExpire() (*time.Time, error) {
	return store.GetBinValue[*time.Time](k.store, keyQuotaExpires, "expire")
}

// ResetAllQuotas will zero the ibc-transfer quotas
func (k Keeper) ResetAllQuotas() error {
	qd := k.GetParams().QuotaDuration
	newExpires := k.blockTime.Add(qd)
	if err := k.SetExpire(newExpires); err != nil {
		return err
	}
	zero := sdk.NewDec(0)
	zeroBz, err := zero.Marshal()
	if err != nil {
		return err
	}
	k.SetTotalOutflowSum(zero)
	store := k.PrefixStore(keyPrefixDenomOutflows)
	iter := sdk.KVStorePrefixIterator(store, nil)
	defer iter.Close()
	for ; iter.Valid(); iter.Next() {
		store.Set(iter.Key(), zeroBz)
	}
	return nil
}

// CheckAndUpdateQuota checks if adding a newOutflow doesn't exceed the max quota and
// updates the current quota metrics.
func (k Keeper) CheckAndUpdateQuota(denom string, newOutflow sdkmath.Int) error {
	params := k.GetParams()
	exchangePrice, err := k.getExchangePrice(denom, newOutflow)
	if err != nil {
		if ltypes.ErrNotRegisteredToken.Is(err) {
			return nil
		} else if err != nil {
			return err
		}
	}

	o := k.GetTokenOutflows(denom)
	o.Amount = o.Amount.Add(exchangePrice)
	if !params.TokenQuota.IsZero() && o.Amount.GT(params.TokenQuota) {
		return uibc.ErrQuotaExceeded
	}

	totalOutflowSum := k.GetTotalOutflow().Add(exchangePrice)
	if !params.TotalQuota.IsZero() && totalOutflowSum.GT(params.TotalQuota) {
		return uibc.ErrQuotaExceeded
	}

	k.SetTokenOutflow(o)
	k.SetTotalOutflowSum(totalOutflowSum)
	return nil
}

func (k Keeper) getExchangePrice(denom string, amount sdkmath.Int) (sdk.Dec, error) {
	transferCoin := sdk.NewCoin(denom, amount)
	var (
		err          error
		exchangeRate sdk.Dec
	)

	// convert to base asset if it is `uToken`
	if ltypes.HasUTokenPrefix(denom) {
		// NOTE: to avoid ctx, we can use similar approach: create a leverage keeper builder
		transferCoin, err = k.leverage.ExchangeUToken(*k.ctx, transferCoin)
		if err != nil {
			return sdk.Dec{}, err
		}
	}

	ts, err := k.leverage.GetTokenSettings(*k.ctx, transferCoin.Denom)
	if err != nil {
		return sdk.Dec{}, err
	}

	// get the exchange price (eg: UMEE) in USD from oracle using SYMBOL Denom eg: `UMEE` (uumee)
	exchangeRate, err = k.oracle.Price(*k.ctx, strings.ToUpper(ts.SymbolDenom))
	if err != nil {
		return sdk.Dec{}, err
	}
	// calculate total exchange rate
	powerReduction := ten.Power(uint64(ts.Exponent))
	return sdk.NewDecFromInt(transferCoin.Amount).Quo(powerReduction).Mul(exchangeRate), nil
}

// UndoUpdateQuota subtracts `amount` from quota metric of the ibc denom.
func (k Keeper) UndoUpdateQuota(denom string, amount sdkmath.Int) error {
	o := k.GetTokenOutflows(denom)
	exchangePrice, err := k.getExchangePrice(denom, amount)
	if err != nil {
		// Note: skip the ibc-transfer quota checking if `denom` is not support by leverage
		if ltypes.ErrNotRegisteredToken.Is(err) {
			return nil
		} else if err != nil {
			return err
		}
	}

	// We ignore the update if the result is negative (due to quota reset on epoch)
	o.Amount = o.Amount.Sub(exchangePrice)
	if o.Amount.IsNegative() {
		return nil
	}
	k.SetTokenOutflow(o)

	totalOutflowSum := k.GetTotalOutflow()
	k.SetTotalOutflowSum(totalOutflowSum.Sub(exchangePrice))
	return nil
}

// CheckIBCInflow validates if inflow token is registered in x/leverage
func (k Keeper) CheckIBCInflow(ctx sdk.Context,
	packet channeltypes.Packet, dataDenom string, isSourceChain bool,
) exported.Acknowledgement {
	// if chain is recevier and sender chain is source then we need create ibc_denom (ibc/hash(channel,denom)) to
	// check ibc_denom is exists in leverage token registry
	if isSourceChain {
		// since SendPacket did not prefix the denomination, we must prefix denomination here
		sourcePrefix := transfertypes.GetDenomPrefix(packet.GetDestPort(), packet.GetDestChannel())
		// NOTE: sourcePrefix contains the trailing "/"
		prefixedDenom := sourcePrefix + dataDenom
		// construct the denomination trace from the full raw denomination and get the ibc_denom
		ibcDenom := transfertypes.ParseDenomTrace(prefixedDenom).IBCDenom()
		_, err := k.leverage.GetTokenSettings(ctx, ibcDenom)
		if err != nil {
			if ltypes.ErrNotRegisteredToken.Is(err) {
				return channeltypes.NewErrorAcknowledgement(err)
			}
			// other leverage keeper error -> log the error  and allow the inflow transfer.
			ctx.Logger().Error("IBC inflows: can't load token registry", "err", err)
		}
	}

	return nil
}

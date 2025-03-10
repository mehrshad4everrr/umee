package ugov

import (
	fmt "fmt"
	"testing"

	sdk "github.com/cosmos/cosmos-sdk/types"
	authtypes "github.com/cosmos/cosmos-sdk/x/auth/types"
	govtypes "github.com/cosmos/cosmos-sdk/x/gov/types"
	"github.com/stretchr/testify/require"

	"github.com/umee-network/umee/v5/tests/accs"
	"github.com/umee-network/umee/v5/util/coin"
)

func validMsgGovUpdateMinGasPrice() MsgGovUpdateMinGasPrice {
	return MsgGovUpdateMinGasPrice{
		Authority:   authtypes.NewModuleAddress("gov").String(),
		MinGasPrice: coin.Atom1_25dec,
	}
}

func TestMsgGovUpdateMinGasPrice(t *testing.T) {
	t.Parallel()
	require := require.New(t)

	msg := validMsgGovUpdateMinGasPrice()
	require.NoError(msg.ValidateBasic())

	require.Equal(
		`<authority: umee10d07y265gmmuvt4z0w9aw880jnsr700jg5w6jp, min_gas_price: 1.250000000000000000ibc/C4CFF46FD6DE35CA4CF4CE031E643C8FDC9BA4B99AE598E9B0ED98FE3A2319F9>`,
		msg.String())

	signers := msg.GetSigners()
	require.Len(signers, 1)
	require.Equal(msg.Authority, signers[0].String())

	msg.MinGasPrice.Amount = sdk.MustNewDecFromStr("0.0000123")
	require.NoError(msg.ValidateBasic(), "fractional amount should be allowed")

	msg.MinGasPrice.Amount = sdk.NewDec(0)
	require.NoError(msg.ValidateBasic(), "zero amount should be allowed")

	// error cases
	msg.MinGasPrice.Amount = sdk.NewDec(-1)
	require.Error(msg.ValidateBasic(), "must error on negative amount")

	msg = validMsgGovUpdateMinGasPrice()
	msg.Authority = accs.Alice.String()
	require.ErrorIs(msg.ValidateBasic(), govtypes.ErrInvalidSigner, "must fail on a non gov account")
}

func validMsgGovSetEmergencyGroup() MsgGovSetEmergencyGroup {
	return MsgGovSetEmergencyGroup{
		Authority:      authtypes.NewModuleAddress("gov").String(),
		EmergencyGroup: accs.Alice.String(),
	}
}

func TestMsgGovSetEmergencyGroup(t *testing.T) {
	t.Parallel()
	require := require.New(t)

	msg := validMsgGovSetEmergencyGroup()
	require.Equal(fmt.Sprintf("authority:%q emergency_group:%q ", msg.Authority, msg.EmergencyGroup),
		msg.String())
	require.Contains("MsgGovSetEmergencyGroup", msg.Route())
	require.NoError(msg.ValidateBasic())

	signers := msg.GetSigners()
	require.Len(signers, 1)
	require.Equal(msg.Authority, signers[0].String())

	msg.Authority = accs.Bob.String()
	require.ErrorIs(msg.ValidateBasic(), govtypes.ErrInvalidSigner, "must fail on a non gov account")

	msg = validMsgGovSetEmergencyGroup()
	msg.EmergencyGroup = "umee1yesmdu06f7strl67kjvg2w7t5kacc"
	require.ErrorContains(msg.ValidateBasic(), "bech32 failed", "must fail with bad emergency_group address")
}

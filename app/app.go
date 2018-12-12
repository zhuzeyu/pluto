package app

import (
	"encoding/json"
	"fmt"
	"math/big"

	"github.com/ethereum/go-ethereum/core"
	"github.com/ethereum/go-ethereum/core/state"
	ethTypes "github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/rpc"

	"github.com/zhuzeyu/pluto/ethereum"
	emtTypes "github.com/zhuzeyu/pluto/types"

	errors "github.com/cosmos/cosmos-sdk/types"
	abciTypes "github.com/tendermint/tendermint/abci/types"
	tmLog "github.com/tendermint/tendermint/libs/log"
)

// PlutoApplication implements an ABCI application
type PlutoApplication struct {

	// backend handles the ethereum state machine
	// and wrangles other services started by an ethereum node (eg. tx pool)
	backend *ethereum.Backend // backend ethereum struct

	// a closure to return the latest current state from the ethereum blockchain
	getCurrentState func() (*state.StateDB, error)

	checkTxState *state.StateDB

	// an ethereum rpc client we can forward queries to
	rpcClient *rpc.Client

	// strategy for validator compensation
	strategy *emtTypes.Strategy

	logger tmLog.Logger
}

// NewPlutoApplication creates a fully initialised instance of PlutoApplication
func NewPlutoApplication(backend *ethereum.Backend,
	client *rpc.Client, strategy *emtTypes.Strategy) (*PlutoApplication, error) {

	state, err := backend.Ethereum().BlockChain().State()
	if err != nil {
		return nil, err
	}

	app := &PlutoApplication{
		backend:         backend,
		rpcClient:       client,
		getCurrentState: backend.Ethereum().BlockChain().State,
		checkTxState:    state.Copy(),
		strategy:        strategy,
	}

	if err := app.backend.InitEthState(app.Receiver()); err != nil {
		return nil, err
	}

	return app, nil
}

// SetLogger sets the logger for the ethermint application
func (app *PlutoApplication) SetLogger(log tmLog.Logger) {
	app.logger = log
}

func (app *PlutoApplication) GetLogger() tmLog.Logger {
	return app.logger
}

var bigZero = big.NewInt(0)

// maxTransactionSize is 32KB in order to prevent DOS attacks
const maxTransactionSize = 32768

// Info returns information about the last height and app_hash to the tendermint engine
func (app *PlutoApplication) Info(req abciTypes.RequestInfo) abciTypes.ResponseInfo {
	blockchain := app.backend.Ethereum().BlockChain()
	currentBlock := blockchain.CurrentBlock()
	height := currentBlock.Number()
	hash := currentBlock.TxHash()

	app.logger.Debug("Info", "height", height) // nolint: errcheck

	// This check determines whether it is the first time ethermint gets started.
	// If it is the first time, then we have to respond with an empty hash, since
	// that is what tendermint expects.
	if height.Cmp(bigZero) == 0 {
		return abciTypes.ResponseInfo{
			Data:             "ABCIEthereum",
			LastBlockHeight:  height.Int64(),
			LastBlockAppHash: []byte{},
		}
	}

	return abciTypes.ResponseInfo{
		Data:             "ABCIEthereum",
		LastBlockHeight:  height.Int64(),
		LastBlockAppHash: hash[:],
	}
}

// SetOption sets a configuration option
// #stable - 0.4.0
func (app *PlutoApplication) SetOption(req abciTypes.RequestSetOption) abciTypes.ResponseSetOption {

	app.logger.Debug("SetOption", "key", req.GetKey(), "value", req.GetValue()) // nolint: errcheck
	return abciTypes.ResponseSetOption{}
}

// InitChain initializes the validator set
// #stable - 0.4.0
func (app *PlutoApplication) InitChain(req abciTypes.RequestInitChain) abciTypes.ResponseInitChain {
	app.logger.Debug("InitChain") // nolint: errcheck
	app.SetValidators(req.Validators)
	return abciTypes.ResponseInitChain{}
}

// CheckTx checks a transaction is valid but does not mutate the state
func (app *PlutoApplication) CheckTx(txBytes []byte) abciTypes.ResponseCheckTx {
	tx, err := decodeTx(txBytes)
	if err != nil {
		// nolint: errcheck
		app.logger.Debug("CheckTx: Received invalid transaction", "tx", tx)
		return abciTypes.ResponseCheckTx{
			Code: uint32(errors.CodeInternal),
			Log:  err.Error(),
		}
	}
	app.logger.Debug("CheckTx: Received valid transaction", "tx", tx) // nolint: errcheck

	return app.validateTx(tx)
}

// DeliverTx executes a transaction against the latest state
func (app *PlutoApplication) DeliverTx(txBytes []byte) abciTypes.ResponseDeliverTx {
	tx, err := decodeTx(txBytes)
	if err != nil {
		// nolint: errcheck
		app.logger.Debug("DelivexTx: Received invalid transaction", "tx", tx, "err", err)
		return abciTypes.ResponseDeliverTx{
			Code: uint32(errors.CodeInternal),
			Log:  err.Error(),
		}
	}
	app.logger.Debug("DeliverTx: Received valid transaction", "tx", tx) // nolint: errcheck

	res := app.backend.DeliverTx(tx)
	if res.IsErr() {
		// nolint: errcheck
		app.logger.Error("DeliverTx: Error delivering tx to ethereum backend", "tx", tx,
			"err", err)
		return res
	}
	app.CollectTx(tx)

	return abciTypes.ResponseDeliverTx{
		Code: abciTypes.CodeTypeOK,
	}
}

// BeginBlock starts a new Ethereum block
func (app *PlutoApplication) BeginBlock(beginBlock abciTypes.RequestBeginBlock) abciTypes.ResponseBeginBlock {

	app.logger.Debug("BeginBlock") // nolint: errcheck
	header := beginBlock.GetHeader()
	// update the eth header with the tendermint header!br0ken!!
	app.backend.UpdateHeaderWithTimeInfo(&header)
	return abciTypes.ResponseBeginBlock{}
}

// EndBlock accumulates rewards for the validators and updates them
func (app *PlutoApplication) EndBlock(endBlock abciTypes.RequestEndBlock) abciTypes.ResponseEndBlock {

	app.logger.Debug("EndBlock", "height", endBlock.GetHeight()) // nolint: errcheck
	app.backend.AccumulateRewards(app.strategy)
	return app.GetUpdatedValidators()
}

// Commit commits the block and returns a hash of the current state
func (app *PlutoApplication) Commit() abciTypes.ResponseCommit {
	app.logger.Debug("Commit") // nolint: errcheck
	blockHash, err := app.backend.Commit(app.Receiver())
	if err != nil {
		// nolint: errcheck
		app.logger.Error("Error getting latest ethereum state", "err", err)
		return abciTypes.ResponseCommit{}
	}
	state, err := app.getCurrentState()
	if err != nil {
		app.logger.Error("Error getting latest state", "err", err) // nolint: errcheck
		return abciTypes.ResponseCommit{}
	}

	app.checkTxState = state.Copy()
	return abciTypes.ResponseCommit{
		Data: blockHash[:],
	}
}

// Query queries the state of the EthermintApplication
func (app *PlutoApplication) Query(query abciTypes.RequestQuery) abciTypes.ResponseQuery {
	app.logger.Debug("Query") // nolint: errcheck
	var in jsonRequest
	if err := json.Unmarshal(query.Data, &in); err != nil {
		return abciTypes.ResponseQuery{Code: uint32(errors.CodeInternal),
			Log: err.Error()}
	}
	var result interface{}
	if err := app.rpcClient.Call(&result, in.Method, in.Params...); err != nil {
		return abciTypes.ResponseQuery{Code: uint32(errors.CodeInternal),
			Log: err.Error()}
	}
	bytes, err := json.Marshal(result)
	if err != nil {
		return abciTypes.ResponseQuery{Code: uint32(errors.CodeInternal),
			Log: err.Error()}
	}
	return abciTypes.ResponseQuery{Code: abciTypes.CodeTypeOK, Value: bytes}
}

//-------------------------------------------------------

// validateTx checks the validity of a tx against the blockchain's current state.
// it duplicates the logic in ethereum's tx_pool
func (app *PlutoApplication) validateTx(tx *ethTypes.Transaction) abciTypes.ResponseCheckTx {

	// Heuristic limit, reject transactions over 32KB to prevent DOS attacks
	if tx.Size() > maxTransactionSize {
		return abciTypes.ResponseCheckTx{
			Code: uint32(errors.CodeInternal),
			Log:  core.ErrOversizedData.Error()}
	}

	var signer ethTypes.Signer = ethTypes.FrontierSigner{}
	if tx.Protected() {
		signer = ethTypes.NewEIP155Signer(tx.ChainId())
	}

	// Make sure the transaction is signed properly
	from, err := ethTypes.Sender(signer, tx)
	if err != nil {
		// TODO: Add errors.CodeTypeInvalidSignature ?
		return abciTypes.ResponseCheckTx{
			Code: uint32(errors.CodeInternal),
			Log:  core.ErrInvalidSender.Error()}
	}

	// Transactions can't be negative. This may never happen using RLP decoded
	// transactions but may occur if you create a transaction using the RPC.
	if tx.Value().Sign() < 0 {
		return abciTypes.ResponseCheckTx{
			Code: uint32(errors.CodeInvalidPubKey),
			Log:  core.ErrNegativeValue.Error()}
	}

	currentState := app.checkTxState

	// Make sure the account exist - cant send from non-existing account.
	if !currentState.Exist(from) {
		return abciTypes.ResponseCheckTx{
			Code: uint32(errors.CodeUnknownAddress),
			Log:  core.ErrInvalidSender.Error()}
	}

	// Check the transaction doesn't exceed the current block limit gas.
	gasLimit := app.backend.GasLimit()
	if gasLimit < tx.Gas() {
		return abciTypes.ResponseCheckTx{
			Code: uint32(errors.CodeInternal),
			Log:  core.ErrGasLimitReached.Error()}
	}

	// Check if nonce is not strictly increasing
	nonce := currentState.GetNonce(from)
	if nonce != tx.Nonce() {
		return abciTypes.ResponseCheckTx{
			Code: uint32(errors.CodeInvalidSequence),
			Log: fmt.Sprintf(
				"Nonce not strictly increasing. Expected %d Got %d",
				nonce, tx.Nonce())}
	}

	// Transactor should have enough funds to cover the costs
	// cost == V + GP * GL
	currentBalance := currentState.GetBalance(from)
	if currentBalance.Cmp(tx.Cost()) < 0 {
		return abciTypes.ResponseCheckTx{
			// TODO: Add errors.CodeTypeInsufficientFunds ?
			Code: uint32(errors.CodeInsufficientFunds),
			Log: fmt.Sprintf(
				"Current balance: %s, tx cost: %s",
				currentBalance, tx.Cost())}
	}

	intrGas, err := core.IntrinsicGas(tx.Data(), tx.To() == nil, true) // homestead == true

	if err != nil && tx.Gas() < intrGas {
		return abciTypes.ResponseCheckTx{
			Code: uint32(errors.CodeInsufficientCoins),
			Log:  err.Error()}
	}

	// Update ether balances
	// amount + gasprice * gaslimit
	currentState.SubBalance(from, tx.Cost())
	// tx.To() returns a pointer to a common address. It returns nil
	// if it is a contract creation transaction.
	if to := tx.To(); to != nil {
		currentState.AddBalance(*to, tx.Value())
	}
	currentState.SetNonce(from, tx.Nonce()+1)

	return abciTypes.ResponseCheckTx{Code: abciTypes.CodeTypeOK}
}

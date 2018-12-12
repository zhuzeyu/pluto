package ethereum

import (
	"bytes"
	"fmt"
	"time"

	"github.com/ethereum/go-ethereum/core"
	ethTypes "github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/log"
	abciTypes "github.com/tendermint/tendermint/abci/types"
	ctypes "github.com/tendermint/tendermint/rpc/core/types"
	rpcClient "github.com/tendermint/tendermint/rpc/lib/client"
	tmTypes "github.com/tendermint/tendermint/types"
)

//----------------------------------------------------------------------
// Transactions sent via the go-ethereum rpc need to be routed to tendermint

// listen for txs and forward to tendermint
func (b *Backend) txBroadcastLoop() {
	//b.txSub = b.ethereum.EventMux().Subscribe(core.TxPreEvent{})
	ch := make(chan core.NewTxsEvent, 100)
	sub := b.ethereum.TxPool().SubscribeNewTxsEvent(ch)
	defer close(ch)
	defer sub.Unsubscribe()

	waitForServer(b.client)

	//for obj := range b.txSub.Chan() {
	for obj := range ch {
		if err := b.BroadcastTx(obj.Txs); err != nil {
			log.Error("Broadcast error", "err", err)
		}
	}
}

func (b *Backend) BroadcastTxSync(tx tmTypes.Tx) (*ctypes.ResultBroadcastTx, error) {
	resCh := make(chan *abciTypes.Response, 1)
	err := b.memPool.CheckTx(tx, func(res *abciTypes.Response) {
		resCh <- res
	})
	if err != nil {
		return nil, fmt.Errorf("Error broadcasting transaction: %v", err)
	}
	res := <-resCh
	r := res.GetCheckTx()
	return &ctypes.ResultBroadcastTx{
		Code: r.Code,
		Data: r.Data,
		Log:  r.Log,
		Hash: tx.Hash(),
	}, nil
}

// BroadcastTx broadcasts a transaction to tendermint core
// #unstable
func (b *Backend) BroadcastTx(txs []*ethTypes.Transaction) error {
	var result interface{}

	buf := new(bytes.Buffer)
	for _, tx := range txs {
		if err := tx.EncodeRLP(buf); err != nil {
			return err
		}
	}
	params := map[string]interface{}{
		"tx": buf.Bytes(),
	}

	_, err := b.client.Call("broadcast_tx_sync", params, &result)
	return err
}

//----------------------------------------------------------------------
// wait for Tendermint to open the socket and run http endpoint

func waitForServer(c rpcClient.HTTPClient) {
	var result interface{}
	for {
		_, err := c.Call("status", map[string]interface{}{}, &result)
		if err == nil {
			break
		}

		log.Info("Waiting for tendermint endpoint to start", "err", err)
		time.Sleep(time.Second * 3)
	}
}

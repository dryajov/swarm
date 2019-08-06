// Copyright 2019 The Swarm Authors
// This file is part of the Swarm library.
//
// The Swarm library is free software: you can redistribute it and/or modify
// it under the terms of the GNU Lesser General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.
//
// The Swarm library is distributed in the hope that it will be useful,
// but WITHOUT ANY WARRANTY; without even the implied warranty of
// MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the
// GNU Lesser General Public License for more details.
//
// You should have received a copy of the GNU Lesser General Public License
// along with the Swarm library. If not, see <http://www.gnu.org/licenses/>.

package swap

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/log"
	"github.com/ethereum/go-ethereum/node"
	"github.com/ethereum/go-ethereum/p2p"
	"github.com/ethereum/go-ethereum/p2p/enode"
	"github.com/ethereum/go-ethereum/rpc"
	contract "github.com/ethersphere/swarm/contracts/swap"
	"github.com/ethersphere/swarm/p2p/protocols"
	p2ptest "github.com/ethersphere/swarm/p2p/testing"
)

/*
TestHandshake creates two mock nodes and initiates an exchange;
it expects a handshake to take place between the two nodes
(the handshake would fail because we don't actually use real nodes here)
*/
func TestHandshake(t *testing.T) {
	var err error

	// setup test swap object
	swap, clean := newTestSwap(t, ownerKey)
	defer clean()

	ctx := context.Background()
	err = testDeploy(ctx, swap.backend, swap)
	if err != nil {
		t.Fatal(err)
	}
	// setup the protocolTester, which will allow protocol testing by sending messages
	protocolTester := p2ptest.NewProtocolTester(swap.owner.privateKey, 2, swap.run)

	// shortcut to creditor node
	debitor := protocolTester.Nodes[0]
	creditor := protocolTester.Nodes[1]

	// set balance artifially
	swap.balances[creditor.ID()] = -42

	// create the expected cheque to be received
	cheque := newTestCheque()

	// sign the cheque
	cheque.Signature, err = cheque.Sign(swap.owner.privateKey)
	if err != nil {
		t.Fatal(err)
	}

	// run the exchange:
	// trigger a `EmitChequeMsg`
	// expect HandshakeMsg on each node
	err = protocolTester.TestExchanges(p2ptest.Exchange{
		Label: "TestHandshake",
		Triggers: []p2ptest.Trigger{
			{
				Code: 1,
				Msg: &EmitChequeMsg{
					Cheque: cheque,
				},
				Peer: debitor.ID(),
			},
		},
		Expects: []p2ptest.Expect{
			{
				Code: 0,
				Msg: &HandshakeMsg{
					ContractAddress: swap.owner.Contract,
				},
				Peer: creditor.ID(),
			},
			{
				Code: 0,
				Msg: &HandshakeMsg{
					ContractAddress: swap.owner.Contract,
				},
				Peer: debitor.ID(),
			},
		},
	})

	// there should be no error at this point
	if err != nil {
		t.Fatal(err)
	}
}

// TestEmitCheque is a full round of a cheque exchange between peers via the protocol.
// We create two swap, for the creditor (beneficiary) and debitor (issuer) each,
// and deploy them to the simulated backend.
// We then create Swap protocol peers with a MsgPipe to be able to directly write messages to each other.
// We have the debitor send a cheque via an `EmitChequeMsg`, then the creditor "reads" (pipe) the message
// and handles the cheque.
func TestEmitCheque(t *testing.T) {
	log.Debug("set up test swaps")
	creditorSwap, clean1 := newTestSwap(t, beneficiaryKey)
	debitorSwap, clean2 := newTestSwap(t, ownerKey)
	defer clean1()
	defer clean2()

	ctx := context.Background()

	log.Debug("deploy to simulated backend")
	err := testDeploy(ctx, creditorSwap.backend, creditorSwap)
	if err != nil {
		t.Fatal(err)
	}
	err = testDeploy(ctx, debitorSwap.backend, debitorSwap)
	if err != nil {
		t.Fatal(err)
	}

	log.Debug("create peer instances")

	// create the debitor peer
	dPtpPeer := p2p.NewPeer(enode.ID{}, "debitor", []p2p.Cap{})
	dProtoPeer := protocols.NewPeer(dPtpPeer, nil, Spec)
	debitor := NewPeer(dProtoPeer, creditorSwap, debitorSwap.owner.address, debitorSwap.owner.Contract)

	// set balance artificially
	creditorSwap.balances[debitor.ID()] = 42
	log.Debug("balance", "balance", creditorSwap.balances[debitor.ID()])
	// a safe check: at this point no cheques should be in the swap
	if len(creditorSwap.cheques) != 0 {
		t.Fatalf("Expected no cheques at creditor, but there are %d:", len(creditorSwap.cheques))
	}

	log.Debug("create a cheque")
	cheque := &Cheque{
		ChequeParams: ChequeParams{
			Contract:    debitorSwap.owner.Contract,
			Beneficiary: creditorSwap.owner.address,
			Amount:      42,
			Honey:       42,
			Timeout:     0,
		},
	}
	cheque.Signature, err = cheque.Sign(debitorSwap.owner.privateKey)
	if err != nil {
		t.Fatal(err)
	}

	emitMsg := &EmitChequeMsg{
		Cheque: cheque,
	}
	// setup the wait for mined transaction function for testing
	cleanup := setupContractTest()
	defer cleanup()

	// now we need to create the channel...
	testBackend.submitDone = make(chan struct{})
	err = creditorSwap.handleEmitChequeMsg(ctx, debitor, emitMsg)
	if err != nil {
		t.Fatal(err)
	}
	// ...on which we wait until the submitChequeAndCash is actually terminated (ensures proper nounce count)
	select {
	case <-testBackend.submitDone:
		log.Debug("submit and cash transactions completed and committed")
	case <-time.After(4 * time.Second):
		t.Fatalf("Timeout waiting for submit and cash transactions to complete")
	}
	log.Debug("balance", "balance", creditorSwap.balances[debitor.ID()])
	// check that the balance has been reset
	if creditorSwap.balances[debitor.ID()] != 0 {
		t.Fatalf("Expected debitor balance to have been reset to %d, but it is %d", 0, creditorSwap.balances[debitor.ID()])
	}
	/*
			TODO: This test actually fails now, because the two Swaps create independent backends,
			thus when handling the cheque, it will actually complain (check ERROR log output)
			with `error="no contract code at given address"`.
			Therefore, the `lastReceivedCheque` is not being saved, and this check would fail.
			So TODO is to find out how to address this (should be by having same backend when creating the Swap)
		if creditorSwap.loadLastReceivedCheque(debitor.ID()) != cheque {
			t.Fatalf("Expected exactly one cheque at creditor, but there are %d:", len(creditorSwap.cheques))
		}
	*/
}

// TestTriggerPaymentThreshold is to test that the whole cheque protocol is triggered
// when we reach the payment threshold
// It is the debitor who triggers cheques
func TestTriggerPaymentThreshold(t *testing.T) {
	log.Debug("create test swap")
	debitorSwap, clean := newTestSwap(t, ownerKey)
	defer clean()

	// setup the wait for mined transaction function for testing
	cleanup := setupContractTest()
	defer cleanup()

	// create a dummy pper
	cPeer := newDummyPeerWithSpec(Spec)
	creditor := NewPeer(cPeer.Peer, debitorSwap, common.Address{}, common.Address{})
	// set the creditor as peer into the debitor's swap
	debitorSwap.peers[creditor.ID()] = creditor

	// set the balance to manually be at PaymentThreshold
	overDraft := 42
	debitorSwap.balances[creditor.ID()] = 0 - DefaultPaymentThreshold

	// we expect a cheque at the end of the test, but not yet
	lenCheques := len(debitorSwap.cheques)
	if lenCheques != 0 {
		t.Fatalf("Expected no cheques yet, but there are %d", lenCheques)
	}
	// do some accounting, no error expected, just a WARN
	err := debitorSwap.Add(int64(-overDraft), creditor.Peer)
	if err != nil {
		t.Fatal(err)
	}

	// we should now have a cheque
	lenCheques = len(debitorSwap.cheques)
	if lenCheques != 1 {
		t.Fatalf("Expected one cheque, but there are %d", lenCheques)
	}
	cheque := debitorSwap.cheques[creditor.ID()]
	expectedAmount := uint64(overDraft) + DefaultPaymentThreshold
	if cheque.Amount != expectedAmount {
		t.Fatalf("Expected cheque amount to be %d, but is %d", expectedAmount, cheque.Amount)
	}

}

// TestTriggerDisconnectThreshold is to test that no further accounting takes place
// when we reach the disconnect threshold
// It is the creditor who triggers the disconnect from a overdraft creditor
func TestTriggerDisconnectThreshold(t *testing.T) {
	log.Debug("create test swap")
	creditorSwap, clean := newTestSwap(t, beneficiaryKey)
	defer clean()

	// create a dummy pper
	cPeer := newDummyPeerWithSpec(Spec)
	debitor := NewPeer(cPeer.Peer, creditorSwap, common.Address{}, common.Address{})
	// set the debitor as peer into the creditor's swap
	creditorSwap.peers[debitor.ID()] = debitor

	// set the balance to manually be at DisconnectThreshold
	overDraft := 42
	expectedBalance := int64(DefaultDisconnectThreshold)
	// we don't expect any change after the test
	creditorSwap.balances[debitor.ID()] = expectedBalance
	// we also don't expect any cheques yet
	lenCheques := len(creditorSwap.cheques)
	if lenCheques != 0 {
		t.Fatalf("Expected no cheques yet, but there are %d", lenCheques)
	}
	// now do some accounting
	err := creditorSwap.Add(int64(overDraft), debitor.Peer)
	// it should fail due to overdraft
	if err == nil {
		t.Fatal("Expected an error due to overdraft, but did not get any")
	}
	// no balance change expected
	if creditorSwap.balances[debitor.ID()] != expectedBalance {
		t.Fatalf("Expected balance to be %d, but is %d", expectedBalance, creditorSwap.balances[debitor.ID()])
	}
	// still no cheques expected
	lenCheques = len(creditorSwap.cheques)
	if lenCheques != 0 {
		t.Fatalf("Expected still no cheque, but there are %d", lenCheques)
	}

	// let's do the whole thing again (actually a bit silly, it's somehow simulating the peer would have been dropped)
	err = creditorSwap.Add(int64(overDraft), debitor.Peer)
	if err == nil {
		t.Fatal("Expected an error due to overdraft, but did not get any")
	}

	if creditorSwap.balances[debitor.ID()] != expectedBalance {
		t.Fatalf("Expected balance to be %d, but is %d", expectedBalance, creditorSwap.balances[debitor.ID()])
	}

	lenCheques = len(creditorSwap.cheques)
	if lenCheques != 0 {
		t.Fatalf("Expected still no cheque, but there are %d", lenCheques)
	}
}

// TestSwapRPC tests some basic things over RPC
// We want this so that we can check the API works
func TestSwapRPC(t *testing.T) {

	var (
		p2pPort = 30100
		ipcPath = ".swarm.ipc"
		err     error
	)

	swap, clean := newTestSwap(t, ownerKey)
	defer clean()

	// need to have a dummy contract or the call will fail at `GetParams` due to `NewAPI`
	swap.contract, err = contract.InstanceAt(common.Address{}, swap.backend)
	if err != nil {
		t.Fatal(err)
	}

	// start a service stack
	stack := createAndStartSvcNode(swap, ipcPath, p2pPort, t)
	defer stack.Stop()

	// connect to the servicenode RPCs
	rpcclient, err := rpc.Dial(filepath.Join(stack.DataDir(), ipcPath))
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(stack.DataDir())

	// create dummy peers so that we can artificially set balances and query
	dummyPeer1 := newDummyPeer()
	dummyPeer2 := newDummyPeer()
	id1 := dummyPeer1.ID()
	id2 := dummyPeer2.ID()

	// set some fake balances
	fakeBalance1 := int64(234)
	fakeBalance2 := int64(-100)

	// query a first time, should be zero
	var balance int64
	err = rpcclient.Call(&balance, "swap_balance", id1)
	if err != nil {
		t.Fatal(err)
	}
	log.Debug("servicenode balance", "balance", balance)

	if balance != 0 {
		t.Fatalf("Expected balance to be 0 but it is %d", balance)
	}

	// now artificially assign some balances
	swap.balances[id1] = fakeBalance1
	swap.balances[id2] = fakeBalance2

	// query them, values should coincide
	err = rpcclient.Call(&balance, "swap_balance", id1)
	if err != nil {
		t.Fatal(err)
	}
	log.Debug("balance1", "balance1", balance)
	if balance != fakeBalance1 {
		t.Fatalf("Expected balance %d to be equal to fake balance %d, but it is not", balance, fakeBalance1)
	}

	err = rpcclient.Call(&balance, "swap_balance", id2)
	if err != nil {
		t.Fatal(err)
	}
	log.Debug("balance2", "balance2", balance)
	if balance != fakeBalance2 {
		t.Fatalf("Expected balance %d to be equal to fake balance %d, but it is not", balance, fakeBalance2)
	}

	// now call all balances
	allBalances := make(map[enode.ID]int64)
	err = rpcclient.Call(&allBalances, "swap_balances")
	if err != nil {
		t.Fatal(err)
	}
	log.Debug("received balances", "allBalances", allBalances)

	var sum int64
	for _, v := range allBalances {
		sum += v
	}

	fakeSum := fakeBalance1 + fakeBalance2
	if sum != int64(fakeSum) {
		t.Fatalf("Expected total balance to be %d, but it %d", fakeSum, sum)
	}

	if !reflect.DeepEqual(allBalances, swap.balances) {
		t.Fatal("Balances are not deep equal")
	}
}

// createAndStartSvcNode setup a p2p service and start it
func createAndStartSvcNode(swap *Swap, ipcPath string, p2pPort int, t *testing.T) *node.Node {
	stack, err := newServiceNode(p2pPort, ipcPath, 0, 0)
	if err != nil {
		t.Fatal("Create servicenode #1 fail", "err", err)
	}

	swapsvc := func(ctx *node.ServiceContext) (node.Service, error) {
		return swap, nil
	}

	err = stack.Register(swapsvc)
	if err != nil {
		t.Fatal("Register service in servicenode #1 fail", "err", err)
	}

	// start the nodes
	err = stack.Start()
	if err != nil {
		t.Fatal("servicenode #1 start failed", "err", err)
	}

	return stack
}

// newServiceNode creates a p2p.Service node stub
func newServiceNode(port int, ipcPath string, httpport int, wsport int, modules ...string) (*node.Node, error) {
	var datadirPrefix = ".data_"
	cfg := &node.DefaultConfig
	cfg.P2P.ListenAddr = fmt.Sprintf(":%d", port)
	cfg.P2P.EnableMsgEvents = true
	cfg.P2P.NoDiscovery = true
	cfg.IPCPath = ipcPath
	cfg.DataDir = fmt.Sprintf("%s%d", datadirPrefix, port)
	if httpport > 0 {
		cfg.HTTPHost = node.DefaultHTTPHost
		cfg.HTTPPort = httpport
	}
	if wsport > 0 {
		cfg.WSHost = node.DefaultWSHost
		cfg.WSPort = wsport
		cfg.WSOrigins = []string{"*"}
		for i := 0; i < len(modules); i++ {
			cfg.WSModules = append(cfg.WSModules, modules[i])
		}
	}
	stack, err := node.New(cfg)
	if err != nil {
		return nil, fmt.Errorf("ServiceNode create fail: %v", err)
	}
	return stack, nil
}

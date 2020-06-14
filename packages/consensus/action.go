package consensus

import (
	"github.com/iotaledger/wasp/packages/committee"
	"github.com/iotaledger/wasp/packages/registry"
	"github.com/iotaledger/wasp/packages/sctransaction"
	"github.com/iotaledger/wasp/packages/state"
	"github.com/iotaledger/wasp/packages/util"
	"github.com/iotaledger/wasp/packages/vm"
	"github.com/iotaledger/wasp/plugins/nodeconn"
	"time"
)

func (op *operator) takeAction() {
	op.requestOutputsIfNeeded()
	if op.iAmCurrentLeader() {
		op.startProcessingIfNeeded()
	}
	op.checkQuorum()
	op.rotateLeaderIfNeeded()
}

func (op *operator) rotateLeaderIfNeeded() {
	if op.leaderRotationDeadlineSet && op.leaderRotationDeadline.Before(time.Now()) {
		prevlead, _ := op.currentLeader()
		leader := op.moveToNextLeader()
		op.log.Debugf("LEADER ROTATED #%d --> #%d", prevlead, leader)
		op.sendRequestNotificationsToLeader(nil)
	}
}

func (op *operator) startProcessingIfNeeded() {
	if op.leaderStatus != nil {
		// request already selected and calculations initialized
		return
	}
	if !op.processorReady {
		return
	}
	reqs := op.selectRequestsToProcess()
	if len(reqs) == 0 {
		// can't select request to process
		//op.log.Debugf("can't select request to process")
		return
	}
	reqIds := takeIds(reqs)
	reqIdsStr := idsShortStr(reqIds)
	op.log.Debugw("requests selected to process",
		"stateIdx", op.stateTx.MustState().StateIndex(),
		"batch", reqIdsStr,
	)
	rewardAddress := registry.GetRewardAddress(op.committee.Address())

	// send to subordinate the request to process the batch
	msgData := util.MustBytes(&committee.StartProcessingReqMsg{
		PeerMsgHeader: committee.PeerMsgHeader{
			// timestamp is set by SendMsgToCommitteePeers
			StateIndex: op.stateTx.MustState().StateIndex(),
		},
		RewardAddress: *rewardAddress,
		Balances:      op.balances,
		RequestIds:    reqIds,
	})

	numSucc, ts := op.committee.SendMsgToCommitteePeers(committee.MsgStartProcessingRequest, msgData)

	op.log.Debugf("%d 'msgStartProcessingRequest' messages sent to peers", numSucc)

	if numSucc < op.quorum()-1 {
		// doesn't make sense to continue because less than quorum sends succeeded
		op.log.Errorf("only %d 'msgStartProcessingRequest' sends succeeded.", numSucc)
		return
	}
	batchHash := vm.BatchHash(reqIds, ts)
	op.leaderStatus = &leaderStatus{
		reqs:          reqs,
		batchHash:     batchHash,
		balances:      op.balances,
		timestamp:     ts,
		signedResults: make([]*signedResult, op.committee.Size()),
	}
	op.log.Debugw("runCalculationsAsync leader",
		"batch hash", batchHash.String(),
		"batch", reqIdsStr,
		"ts", ts,
	)
	// process the batch on own side
	op.runCalculationsAsync(runCalculationsParams{
		requests:        reqs,
		leaderPeerIndex: op.committee.OwnPeerIndex(),
		balances:        op.balances,
		timestamp:       ts,
		rewardAddress:   *rewardAddress,
	})
}

func (op *operator) checkQuorum() bool {
	if op.leaderStatus == nil || op.leaderStatus.resultTx == nil || op.leaderStatus.finalized {
		return false
	}
	// collect signature shares available
	mainHash := op.leaderStatus.signedResults[op.committee.OwnPeerIndex()].essenceHash
	sigShares := make([][]byte, 0, op.committee.Size())
	for i := range op.leaderStatus.signedResults {
		if op.leaderStatus.signedResults[i] == nil {
			continue
		}
		if op.leaderStatus.signedResults[i].essenceHash != mainHash {
			op.log.Warnf("wrong EssenceHash from peer #%d", i)
			op.leaderStatus.signedResults[i] = nil // ignoring
			continue
		}
		err := op.dkshare.VerifySigShare(op.leaderStatus.resultTx.EssenceBytes(), op.leaderStatus.signedResults[i].sigShare)
		if err != nil {
			op.log.Warnf("wrong signature from peer #%d: %v", i, err)
			op.leaderStatus.signedResults[i] = nil // ignoring
			continue
		}

		sigShares = append(sigShares, op.leaderStatus.signedResults[i].sigShare)
	}

	if len(sigShares) < int(op.quorum()) {
		return false
	}
	// quorum detected
	err := op.aggregateSigShares(sigShares)
	if err != nil {
		op.log.Errorf("aggregateSigShares returned: %v", err)
		return false
	}

	if !op.leaderStatus.resultTx.SignaturesValid() {
		op.log.Error("final signature invalid: something went wrong while finalizing result transaction")
		return false
	}

	if err := op.leaderStatus.resultTx.ValidateConsumptionOfInputs(op.committee.Address(), op.leaderStatus.balances); err != nil {
		op.log.Errorf("ValidateConsumptionOfInputs: final tx invalid: %v", err)
		return false
	}

	sh := op.leaderStatus.resultTx.MustState().VariableStateHash()
	op.log.Infof("FINALIZED RESULT. Posting transaction to the Value Tangle. txid = %s state hash = %s",
		op.leaderStatus.resultTx.ID().String(),
		sh.String(),
	)
	op.leaderStatus.finalized = true

	if err = nodeconn.PostTransactionToNode(op.leaderStatus.resultTx.Transaction); err != nil {
		op.log.Warnf("PostTransactionToNode failed: %v", err)
		return false
	}
	//nodeconn.PostTransactionToNodeAsyncWithRetry(op.leaderStatus.resultTx.Transaction, 2*time.Second, 7*time.Second, op.log)
	return true
}

// sets new state transaction and initializes respective variables
func (op *operator) setNewState(stateTx *sctransaction.Transaction, variableState state.VariableState) {
	op.stateTx = stateTx
	op.variableState = variableState

	op.requestBalancesDeadline = time.Now()
	op.requestOutputsIfNeeded()

	op.resetLeader(stateTx.ID().Bytes())

	// if consistently moving to the next state, computation requests and notifications about
	// requests for the next state index are brought to the current state next state list is cleared
	// otherwise any state data is cleared
	if op.variableState != nil && variableState.StateIndex() == op.variableState.StateIndex()+1 {
		op.currentStateCompRequests, op.nextStateCompRequests =
			op.nextStateCompRequests, op.currentStateCompRequests
		op.nextStateCompRequests = op.nextStateCompRequests[:0]
	} else {
		op.nextStateCompRequests = op.nextStateCompRequests[:0]
		op.currentStateCompRequests = op.currentStateCompRequests[:0]
	}

	for _, req := range op.requests {
		setAllFalse(req.notifications)
		req.notifications[op.peerIndex()] = req.reqTx != nil
	}
	// run through the notification backlog and mark relevant notifications
	for _, nmsg := range op.notificationsBacklog {
		if nmsg.StateIndex == op.variableState.StateIndex() {
			for _, rid := range nmsg.RequestIds {
				r, ok := op.requestFromId(rid)
				if !ok {
					continue
				}
				r.notifications[nmsg.SenderIndex] = true
			}
		}
	}
	// clean notification backlog
	op.notificationsBacklog = op.notificationsBacklog[:0]
}

const requestBalancesTimeout = 1 * time.Second

func (op *operator) requestOutputsIfNeeded() {
	if op.balances != nil {
		return
	}
	if op.requestBalancesDeadline.After(time.Now()) {
		return
	}
	if err := nodeconn.RequestOutputsFromNode(op.committee.Address()); err != nil {
		op.log.Debugf("RequestOutputsFromNode failed: %v", err)
	}
	op.requestBalancesDeadline = time.Now().Add(requestBalancesTimeout)
}

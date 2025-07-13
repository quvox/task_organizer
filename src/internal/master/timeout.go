package master

import (
	"time"

	"github.com/quvox/task_organizer/internal/common"
)

// startRequestTimeoutMonitoring はリクエストのタイムアウト監視を開始する
// @obj: 個別リクエストのタイムアウト監視スレッドを起動
// @ref: SCIK9X27-000003-000021, SCIK9X27-000003-000022
func (tm *TaskMaster) startRequestTimeoutMonitoring(requestID string, timeout time.Duration) {
	tm.wg.Add(1)
	go tm.requestTimeoutMonitor(requestID, timeout)
}

// requestTimeoutMonitor は個別リクエストのタイムアウトを監視する
// @obj: 指定時間後にTIMEOUT_CHECKメッセージを送信
// @ref: SCIK9X27-000003-000021, SCIK9X27-000003-000022
func (tm *TaskMaster) requestTimeoutMonitor(requestID string, timeout time.Duration) {
	defer tm.wg.Done()
	
	tm.logger.Debugf("Starting timeout monitor for request %s (timeout: %v)", requestID, timeout)
	
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	
	select {
	case <-timer.C:
		// @obj: タイムアウト発生時にTIMEOUT_CHECKメッセージを送信
		// @ref: SCIK9X27-000003-000022
		tm.logger.Debugf("Timeout reached for request %s", requestID)
		
		timeoutMsg := &InternalMessage{
			Message:  common.NewMessage(common.TypeTimeoutCheck, "Request timeout", requestID),
			WorkerID: "TIMEOUT_MONITOR",
		}
		
		select {
		case tm.msgChan <- timeoutMsg:
			tm.logger.Debugf("Sent TIMEOUT_CHECK message for request %s", requestID)
		case <-time.After(1 * time.Second):
			tm.logger.Warnf("Failed to send TIMEOUT_CHECK message for request %s", requestID)
		case <-tm.ctx.Done():
			return
		}
		
	case <-tm.ctx.Done():
		// マスター終了時にタイムアウト監視も終了
		tm.logger.Debugf("Timeout monitor for request %s stopped due to master shutdown", requestID)
		return
	}
}

// startHealthCheckTimeoutMonitoring はヘルスチェックのタイムアウト監視を開始する
// @obj: 個別ヘルスチェックのタイムアウト監視スレッドを起動
// @ref: SCIK9X27-000003-000031, SCIK9X27-000003-000032
func (tm *TaskMaster) startHealthCheckTimeoutMonitoring(requestID string, workerID string, timeout time.Duration) {
	tm.wg.Add(1)
	go tm.healthCheckTimeoutMonitor(requestID, workerID, timeout)
}

// healthCheckTimeoutMonitor は個別ヘルスチェックのタイムアウトを監視する
// @obj: ヘルスチェック応答がない場合にワーカーを切断
// @ref: SCIK9X27-000003-000031, SCIK9X27-000003-000032
func (tm *TaskMaster) healthCheckTimeoutMonitor(requestID string, workerID string, timeout time.Duration) {
	defer tm.wg.Done()
	
	tm.logger.Debugf("Starting health check timeout monitor for worker %s (request: %s, timeout: %v)", 
		workerID, requestID, timeout)
	
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	
	select {
	case <-timer.C:
		// @obj: アクティブリクエストをチェックして、まだ応答がない場合のみタイムアウト処理
		// @ref: SCIK9X27-000003-000032
		tm.workersMu.RLock()
		worker, exists := tm.workers[workerID]
		tm.workersMu.RUnlock()
		
		if !exists {
			// ワーカーがすでに削除されている
			tm.logger.Debugf("Worker %s already removed, health check timeout monitor stopping", workerID)
			return
		}
		
		worker.mu.Lock()
		_, requestExists := worker.ActiveRequests[requestID]
		worker.mu.Unlock()
		
		if !requestExists {
			// リクエストがすでに処理済み（CHECK_ACK受信済み）
			tm.logger.Debugf("Health check request %s already processed for worker %s", requestID, workerID)
			return
		}
		
		// タイムアウト発生：ワーカーを削除
		tm.logger.Warnf("Health check timeout for worker %s (request: %s)", workerID, requestID)
		tm.removeWorker(workerID)
		
	case <-tm.ctx.Done():
		// マスター終了時にタイムアウト監視も終了
		tm.logger.Debugf("Health check timeout monitor for worker %s stopped due to master shutdown", workerID)
		return
	}
}
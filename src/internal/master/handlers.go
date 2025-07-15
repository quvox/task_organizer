package master

import (
	"os"
	"path/filepath"
	"time"

	"github.com/google/uuid"
	"github.com/quvox/task_organizer/internal/common"
)

// mainLoop はマスターのメインループ
// @obj: メッセージとタイマーイベントを処理する中央制御ループ
// @ref: SCIK9X27-000003-000005, SCIK9X27-000003-00000D
func (tm *TaskMaster) mainLoop() {
	defer tm.wg.Done()
	tm.logger.Debugf("[MASTER] @@@@@ MAIN LOOP STARTED @@@@@")

	for {
		tm.logger.Debugf("[MASTER] @@@@@ MAIN LOOP WAITING FOR MESSAGE, ChannelLen=%d @@@@@", len(tm.msgChan))
		select {
		case <-tm.ctx.Done():
			// @obj: グレースフルシャットダウン
			// @ref: SCIK9X27-000003-00001E
			tm.logger.Debugf("[MASTER] @@@@@ MAIN LOOP RECEIVED SHUTDOWN SIGNAL @@@@@")
			tm.handleShutdown()
			return

		case msg := <-tm.msgChan:
			// @obj: ワーカーからのメッセージ処理（内部メッセージを含む）
			// @ref: SCIK9X27-000003-00000E
			tm.logger.Debugf("[MASTER] @@@@@ MAIN LOOP RECEIVED MESSAGE: Type=%s, WorkerID=%s, ChannelLen=%d @@@@@", msg.Message.Type, msg.WorkerID, len(tm.msgChan))
			tm.handleWorkerMessage(msg)
			tm.logger.Debugf("[MASTER] @@@@@ MAIN LOOP FINISHED PROCESSING MESSAGE: Type=%s, ChannelLen=%d @@@@@", msg.Message.Type, len(tm.msgChan))
		}
	}
}

// handleWorkerMessage はワーカーからのメッセージを処理する
// @obj: メッセージタイプに応じた処理の振り分け
// @ref: SCIK9X27-000003-00000E, SCIK9X27-000003-000015
func (tm *TaskMaster) handleWorkerMessage(msg *InternalMessage) {
	tm.logger.Debugf("[MASTER] ##### Processing message from %s: %s #####", msg.WorkerID, msg.Message.Type)
	
	// 内部メッセージ（TIMER_THREAD等）の場合は直接処理
	if msg.WorkerID == "TIMER_THREAD" {
		switch msg.Message.Type {
		case common.TypeTimer:
			tm.handleTimerEvent()
		case common.TypeCompleted:
			// @obj: COMPLETEDメッセージを受信した場合は、メインループを抜けて、タスク管理マスタ終了処理を実施
			// @ref: SCIK9X27-000003-000026
			tm.logger.Info("Received COMPLETED message, starting shutdown")
			tm.cancel()
		default:
			tm.logger.Warnf("Unknown internal message type: %s", msg.Message.Type)
		}
		return
	}

	tm.workersMu.Lock()
	worker, exists := tm.workers[msg.WorkerID]
	tm.workersMu.Unlock()

	// DONE/FAILEDメッセージは、ワーカーが既に削除されていても処理する
	if !exists && msg.Message.Type != common.TypeDone && msg.Message.Type != common.TypeFailed {
		tm.logger.Warnf("Received message from unknown worker: %s (message type: %s)", msg.WorkerID, msg.Message.Type)
		return
	}
	
	if exists {
		tm.logger.Debugf("[MASTER] Worker %s exists, processing %s message", msg.WorkerID, msg.Message.Type)
	} else {
		tm.logger.Debugf("[MASTER] Worker %s does not exist, but processing %s message anyway", msg.WorkerID, msg.Message.Type)
	}

	tm.logger.Debugf("[MASTER] @@@@@ ENTERING SWITCH STATEMENT FOR MESSAGE TYPE: %s @@@@@", msg.Message.Type)
	switch msg.Message.Type {
	case common.TypeRequestAck:
		// @obj: タスク実行依頼承諾の処理
		// @ref: SCIK9X27-000003-000009
		tm.logger.Debugf("[MASTER] Processing REQUEST_ACK from %s", worker.ID)
		tm.handleRequestAck(worker, msg.Message)

	case common.TypeDone:
		// @obj: タスク完了報告の処理（同期処理で問題特定）
		// @ref: SCIK9X27-000003-000023
		tm.logger.Debugf("[MASTER] ##### MATCHED DONE CASE IN SWITCH STATEMENT #####")
		tm.logger.Infof("[MASTER] ***** CALLING handleTaskDone for worker %s *****", msg.WorkerID)
		tm.handleTaskDone(worker, msg.Message)

	case common.TypeFailed:
		// @obj: タスク失敗報告の処理（同期処理で問題特定）
		// @ref: SCIK9X27-000003-000024
		tm.handleTaskFailed(worker, msg.Message)

	case common.TypeUsageLimited:
		// @obj: 使用制限報告の処理
		// @ref: SCIK9X27-000003-000025
		tm.handleUsageLimited(worker, msg.Message)

	case common.TypeCheckAck:
		// @obj: ヘルスチェック応答の処理
		// @ref: SCIK9X27-000003-000033
		tm.handleCheckAck(worker, msg.Message)

	case common.TypeLeave:
		// @obj: ワーカー離脱の処理
		// @ref: SCIK9X27-000003-000027
		tm.logger.Debugf("[MASTER] Processing LEAVE from %s", worker.ID)
		tm.handleWorkerLeave(worker)

	case common.TypeDisconnect:
		// @obj: ワーカー切断完了の処理（内部メッセージ）
		// @ref: SCIK9X27-000003-00002B
		tm.handleWorkerDisconnected(msg.WorkerID)

	case common.TypeTimer:
		// @obj: タイマーイベントの処理（内部メッセージ、同期処理で問題特定）
		// @ref: SCIK9X27-000003-00002C
		tm.handleTimerEvent()

	case common.TypeTimeoutCheck:
		// @obj: タイムアウトチェック処理（内部メッセージ）
		// @ref: SCIK9X27-000003-000022
		tm.handleTimeoutCheck(msg.Message)

	default:
		tm.logger.Warnf("Unknown message type from worker %s: %s", msg.WorkerID, msg.Message.Type)
	}
}

// handleRequestAck はREQUEST_ACKメッセージを処理する
// @obj: ワーカーがタスクを受け入れたことを確認
// @ref: SCIK9X27-000003-000009
func (tm *TaskMaster) handleRequestAck(worker *WorkerInfo, msg *common.Message) {
	worker.mu.Lock()
	defer worker.mu.Unlock()

	if worker.State == StateRequesting && worker.RequestID == msg.ReqID {
		worker.State = StateWorking
		worker.RequestID = ""
		tm.logger.Infof("Worker %s acknowledged task request for %s", worker.ID, worker.CurrentTask)
		
		// @obj: アクティブリクエストから削除（既にワーカーmutexを取得済みなので直接削除）
		// @ref: SCIK9X27-000003-000016, SCIK9X27-000003-000017
		if requestInfo, exists := worker.ActiveRequests[msg.ReqID]; exists {
			delete(worker.ActiveRequests, msg.ReqID)
			tm.logger.Debugf("Removed active request %s for worker %s", msg.ReqID, worker.ID)
			_ = requestInfo // 使用済みをマークするため
		}
	}
}

// handleTaskDone はタスク完了を処理する
// @obj: 完了したタスクをdoneディレクトリに移動
// @ref: SCIK9X27-000003-00000A, SCIK9X27-000003-000017
func (tm *TaskMaster) handleTaskDone(worker *WorkerInfo, msg *common.Message) {
	workerID := "unknown"
	if worker != nil {
		workerID = worker.ID
	}
	tm.logger.Debugf("[MASTER] ===== RECEIVED DONE MESSAGE FROM WORKER %s =====", workerID)
	tm.logger.Debugf("[MASTER] >>> MESSAGE CONTENT: %s <<<", msg.String())
	
	// @obj: メッセージからタスクファイル名を取得
	// @ref: SCIK9X27-000001-000033
	taskFile := msg.Msg
	tm.logger.Debugf("[MASTER] Task file from message: '%s'", taskFile)
	
	// ワーカーがまだ存在する場合のみ状態を更新
	if worker != nil {
		worker.mu.Lock()
		currentTask := worker.CurrentTask
		worker.CurrentTask = ""
		worker.State = StateIdle
		worker.mu.Unlock()
		tm.logger.Infof("[MASTER] Worker %s state changed to IDLE (was working on: '%s')", worker.ID, currentTask)
	} else {
		tm.logger.Debugf("[MASTER] Worker already disconnected, processing DONE message for task file only")
	}

	if taskFile == "" {
		return
	}

	// @obj: タスクファイルをdoneディレクトリに移動
	// @ref: SCIK9X27-000003-000017
	workingPath := filepath.Join(tm.rootDir, ".tasks", "working", taskFile)
	donePath := filepath.Join(tm.rootDir, ".tasks", "done", taskFile)
	
	tm.logger.Infof("[MASTER] Moving task file from working to done: %s -> %s", workingPath, donePath)
	if err := os.Rename(workingPath, donePath); err != nil {
		tm.logger.Errorf("[MASTER] !!!!! FAILED TO MOVE TASK %s TO DONE: %v !!!!!", taskFile, err)
	} else {
		tm.logger.Infof("[MASTER] +++++ TASK %s COMPLETED BY WORKER %s +++++", taskFile, workerID)
		// @obj: 成功タスク統計を更新
		// @ref: SCIK9X27-000003-000034
		tm.incrementSuccessTasks()
	}

	// @obj: 全タスク完了チェック（非同期実行でメインループをブロックしない）
	// @ref: SCIK9X27-000003-00001D
	tm.logger.Infof("[MASTER] Checking if all tasks completed (async)...")
	go tm.checkAllTasksCompleted()
	tm.logger.Infof("[MASTER] Task completion check initiated")
	
	// @obj: 即座に次のタスクをアイドルワーカーに割り当て（非同期実行でメインループをブロックしない）
	// @ref: SCIK9X27-000003-000010
	tm.logger.Debugf("[MASTER] ##### ASSIGNING NEXT TASKS TO IDLE WORKERS (ASYNC) #####")
	go tm.assignTasksToIdleWorkers()
	tm.logger.Infof("[MASTER] handleTaskDone processing completed for worker %s", workerID)
}

// handleTaskFailed はタスク失敗を処理する
// @obj: 失敗したタスクをfailedディレクトリに移動し、ワーカー状態をidleに変更
// @ref: SCIK9X27-000003-000024
func (tm *TaskMaster) handleTaskFailed(worker *WorkerInfo, msg *common.Message) {
	// @obj: メッセージからタスクファイル名を取得
	// @ref: SCIK9X27-000001-000034
	taskFile := msg.Msg
	
	workerID := "unknown"
	if worker != nil {
		workerID = worker.ID
		// @obj: ワーカーの状態をidleに変更
		// @ref: SCIK9X27-000003-000024
		worker.mu.Lock()
		worker.CurrentTask = ""
		worker.State = StateIdle
		worker.mu.Unlock()
	}

	if taskFile == "" {
		return
	}

	// @obj: タスクファイルをfailedディレクトリに移動
	// @ref: SCIK9X27-000003-000024
	workingPath := filepath.Join(tm.rootDir, ".tasks", "working", taskFile)
	failedPath := filepath.Join(tm.rootDir, ".tasks", "failed", taskFile)
	
	if err := os.Rename(workingPath, failedPath); err != nil {
		tm.logger.Errorf("Failed to move task %s to failed: %v", taskFile, err)
	} else {
		tm.logger.Warnf("Task %s failed by worker %s", taskFile, workerID)
		// @obj: 失敗タスク統計を更新
		// @ref: SCIK9X27-000003-000034
		tm.incrementFailedTasks()
	}
	
	// @obj: 即座に次のタスクをアイドルワーカーに割り当て（非同期実行でメインループをブロックしない）
	// @ref: SCIK9X27-000003-00001A
	go tm.assignTasksToIdleWorkers()
}

// handleUsageLimited は使用制限エラーを処理する
// @obj: 使用制限に達したワーカーのタスクをpendingに戻し、ワーカーを切断処理
// @ref: SCIK9X27-000003-000025
func (tm *TaskMaster) handleUsageLimited(worker *WorkerInfo, msg *common.Message) {
	// @obj: 現在のタスクを確認
	// @ref: SCIK9X27-000003-000025
	worker.mu.Lock()
	taskFile := worker.CurrentTask
	worker.State = StateDisconnecting
	worker.mu.Unlock()

	// @obj: .tasks/working/の下の当該タスクプロンプトファイルを.tasks/pending/に移動
	// @ref: SCIK9X27-000003-000025
	if taskFile != "" {
		tm.returnTaskToPending(taskFile)
	}

	tm.logger.Warnf("Worker %s reached usage limit", worker.ID)
	
	// @obj: ワーカーに切断通知を送信してから切断処理を実施
	// @ref: SCIK9X27-000003-000025
	disconnectMsg := common.NewMessage(common.TypeDisconn, "Usage limit reached", "")
	if err := tm.sendMessage(worker.Conn, disconnectMsg); err != nil {
		tm.logger.Errorf("Failed to send disconnect message to usage limited worker %s: %v", worker.ID, err)
	} else {
		tm.logger.Infof("Sent disconnect notification to usage limited worker %s", worker.ID)
	}
	
	// @obj: ワーカーオブジェクトに対してワーカー切断処理を実施
	// @ref: SCIK9X27-000003-000025
	tm.removeWorker(worker.ID)
}

// handleCheckAck はヘルスチェック応答を処理する
// @obj: ワーカーの生存確認を更新
// @ref: SCIK9X27-000003-000014
func (tm *TaskMaster) handleCheckAck(worker *WorkerInfo, msg *common.Message) {
	worker.mu.Lock()
	worker.LastHealthCheck = time.Now()
	// @obj: アクティブリクエストから削除（ヘルスチェック完了、既にワーカーmutexを取得済みなので直接削除）
	// @ref: SCIK9X27-000003-000016, SCIK9X27-000003-000017
	if requestInfo, exists := worker.ActiveRequests[msg.ReqID]; exists {
		delete(worker.ActiveRequests, msg.ReqID)
		tm.logger.Debugf("Removed active health check request %s for worker %s", msg.ReqID, worker.ID)
		_ = requestInfo // 使用済みをマークするため
	}
	worker.mu.Unlock()
	tm.logger.Debugf("Health check ACK from worker %s", worker.ID)
}

// handleWorkerLeave はワーカー離脱を処理する
// @obj: ワーカーの正常な離脱処理
// @ref: SCIK9X27-000003-00001C
func (tm *TaskMaster) handleWorkerLeave(worker *WorkerInfo) {
	tm.logger.Infof("Worker %s is leaving", worker.ID)
	tm.removeWorker(worker.ID)
}

// handleWorkerDisconnected はワーカー切断完了を処理する
// @obj: 切断スレッドからのDISCONNECT内部メッセージを処理
// @ref: SCIK9X27-000003-000029, SCIK9X27-000003-000030
func (tm *TaskMaster) handleWorkerDisconnected(workerID string) {
	tm.logger.Infof("Worker %s disconnection completed", workerID)
	tm.removeWorker(workerID)
}

// handleTimerEvent はタイマーイベントを処理する
// @obj: タイマースレッドからのTIMERメッセージを処理し、定期処理を実行
// @ref: SCIK9X27-000003-00002C
func (tm *TaskMaster) handleTimerEvent() {
	tm.logger.Debug("Processing timer event")
	
	// @obj: .tasks/pending/および.tasks/working/以下のファイル数のチェック（非同期実行）
	// @ref: SCIK9X27-000003-00002D, SCIK9X27-000003-00002F
	go tm.checkAllTasksCompleted()
	
	// @obj: タスクワーカーの接続状態のチェック（非同期実行でメインループをブロックしない）
	// @ref: SCIK9X27-000003-00002E
	go tm.sendHealthChecks()
	
	// @obj: 定期的なタスク割り当て処理（非同期実行）
	// @ref: SCIK9X27-000003-00001A
	go tm.assignTasksToIdleWorkers()
	
	// @obj: タイムアウトチェック（非同期実行）
	// @ref: SCIK9X27-000003-000022
	go tm.checkRequestTimeouts()
}

// handleTimeoutCheck はタイムアウトチェックメッセージを処理する
// @obj: タイムアウト監視スレッドからのTIMEOUT_CHECKメッセージを処理
// @ref: SCIK9X27-000003-000021, SCIK9X27-000003-000022
func (tm *TaskMaster) handleTimeoutCheck(msg *common.Message) {
	// メッセージ内容からリクエストIDを取得
	requestID := msg.ReqID
	if requestID == "" {
		tm.logger.Warn("TIMEOUT_CHECK message without request ID")
		return
	}
	
	tm.logger.Debugf("Processing timeout check for request %s", requestID)
	
	// @obj: アクティブリクエスト配列から該当するリクエストを検索
	// @ref: SCIK9X27-000003-000016, SCIK9X27-000003-000017
	targetWorker, requestInfo := tm.findActiveRequest(requestID)
	
	if targetWorker != nil && requestInfo != nil {
		// @obj: タイムアウトしたリクエストをリセット
		// @ref: SCIK9X27-000003-000022
		tm.logger.Warnf("Request %s (type: %s) timed out for worker %s", 
			requestID, requestInfo.Type, targetWorker.ID)
		
		// アクティブリクエストから削除
		tm.removeActiveRequest(targetWorker.ID, requestID)
		
		if requestInfo.Type == common.TypeRequest {
			// タスクリクエストのタイムアウト
			targetWorker.mu.Lock()
			if targetWorker.RequestID == requestID {
				targetWorker.State = StateIdle
				targetWorker.CurrentTask = ""
				targetWorker.RequestID = ""
			}
			targetWorker.mu.Unlock()
			
			// タスクをpendingに戻す
			if requestInfo.TaskFile != "" {
				tm.returnTaskToPending(requestInfo.TaskFile)
			}
		} else if requestInfo.Type == common.TypeCheck {
			// ヘルスチェックのタイムアウト：ワーカーを削除
			tm.logger.Warnf("Removing worker %s due to health check timeout", targetWorker.ID)
			tm.removeWorker(targetWorker.ID)
		}
	}
}

// assignTasksToIdleWorkers はアイドル状態のワーカーにタスクを割り当てる
// @obj: pendingタスクをアイドルワーカーに分配
// @ref: SCIK9X27-000003-000010, SCIK9X27-000003-000015
func (tm *TaskMaster) assignTasksToIdleWorkers() {
	tm.logger.Debugf("[MASTER] ===== STARTING TASK ASSIGNMENT TO IDLE WORKERS =====")
	
	// pendingディレクトリからタスクを取得
	pendingDir := filepath.Join(tm.rootDir, ".tasks", "pending")
	tm.logger.Infof("[MASTER] Reading pending directory: %s", pendingDir)
	entries, err := os.ReadDir(pendingDir)
	if err != nil {
		tm.logger.Errorf("[MASTER] Failed to read pending directory: %v", err)
		return
	}
	tm.logger.Infof("[MASTER] Found %d entries in pending directory", len(entries))

	if len(entries) == 0 {
		tm.logger.Infof("[MASTER] No pending tasks found")
		return
	}

	// アイドルワーカーを取得
	tm.workersMu.RLock()
	idleWorkers := make([]*WorkerInfo, 0)
	for workerID, worker := range tm.workers {
		tm.logger.Infof("[MASTER] Worker %s state: %v", workerID, worker.State)
		if worker.State == StateIdle {
			idleWorkers = append(idleWorkers, worker)
		}
	}
	tm.workersMu.RUnlock()
	tm.logger.Infof("[MASTER] Found %d idle workers out of %d total workers", len(idleWorkers), len(tm.workers))

	// タスクを割り当て
	for i, entry := range entries {
		if i >= len(idleWorkers) {
			break
		}
		if entry.IsDir() {
			continue
		}

		taskFile := entry.Name()
		worker := idleWorkers[i]

		tm.logger.Infof("[MASTER] >>>>> ASSIGNING TASK %s TO WORKER %s <<<<<", taskFile, worker.ID)

		// @obj: 稼働状態がidleになっているワーカオブジェクトを見つける。これを対象ワーカオブジェクトと呼ぶことにする
		// @ref: SCIK9X27-000003-00003C
		
		// @obj: 対象ワーカオブジェクトのタスク稼働状態をrequestingにする
		// @ref: SCIK9X27-000003-00003D
		worker.mu.Lock()
		worker.State = StateRequesting
		worker.mu.Unlock()
		tm.logger.Infof("[MASTER] Worker %s state changed to REQUESTING", worker.ID)

		// @obj: .tasks/pending/の中から1つファイルを取り出して、.tasks/working/に移動する
		// @ref: SCIK9X27-000003-00001C
		taskPath := filepath.Join(pendingDir, taskFile)
		workingPath := filepath.Join(tm.rootDir, ".tasks", "working", taskFile)
		if err := os.Rename(taskPath, workingPath); err != nil {
			tm.logger.Errorf("Failed to move task to working: %v", err)
			worker.mu.Lock()
			worker.State = StateIdle
			worker.mu.Unlock()
			continue
		}

		// タスクの内容を読み込む
		content, err := os.ReadFile(workingPath)
		if err != nil {
			tm.logger.Errorf("Failed to read task file %s: %v", taskFile, err)
			worker.mu.Lock()
			worker.State = StateIdle
			worker.mu.Unlock()
			tm.returnTaskToPending(taskFile)
			continue
		}

		// @obj: 対象ワーカオブジェクトのソケットに対して、今.tasks/working/に移動したタスクプロンプトの実行を依頼するメッセージ（REQUEST）を送る。メッセージには、リクエストID（乱数）とタスクファイル名、プロンプトテキストを含める
		// @ref: SCIK9X27-000003-00003E
		reqID := uuid.New().String()
		// タスクファイル名付きのREQUESTメッセージを作成
		reqMsg := common.NewTaskMessage(common.TypeRequest, string(content), reqID, taskFile)
		
		worker.mu.Lock()
		worker.CurrentTask = taskFile
		worker.RequestTime = time.Now()
		worker.RequestID = reqID
		worker.mu.Unlock()

		tm.logger.Debugf("[MASTER] >>> SENDING REQUEST MESSAGE TO WORKER %s: %s <<<", worker.ID, reqMsg.String())
		if err := tm.sendMessage(worker.Conn, reqMsg); err != nil {
			tm.logger.Errorf("[MASTER] !!!!! FAILED TO SEND TASK REQUEST TO WORKER %s: %v !!!!!", worker.ID, err)
			worker.mu.Lock()
			worker.State = StateIdle
			worker.CurrentTask = ""
			worker.RequestID = ""
			worker.mu.Unlock()
			tm.returnTaskToPending(taskFile)
		} else {
			tm.logger.Infof("[MASTER] +++++ SUCCESSFULLY SENT TASK %s TO WORKER %s +++++", taskFile, worker.ID)
			// @obj: 全タスク数統計を更新
			// @ref: SCIK9X27-000003-000034
			tm.incrementTotalTasks()
			
			// @obj: アクティブリクエストを追加
			// @ref: SCIK9X27-000003-000016, SCIK9X27-000003-000017
			tm.addActiveRequest(worker.ID, reqID, taskFile, common.TypeRequest)
			
			// @obj: リクエストタイムアウト監視を開始（10秒タイムアウト）
			// @ref: SCIK9X27-000001-00004C
			tm.startRequestTimeoutMonitoring(reqID, 10*time.Second)
		}
	}
}

// sendHealthChecks はすべてのワーカーにヘルスチェックを送信する
// @obj: 定期的なワーカー生存確認
// @ref: SCIK9X27-000003-000012, SCIK9X27-000003-000013
func (tm *TaskMaster) sendHealthChecks() {
	tm.workersMu.RLock()
	workers := make([]*WorkerInfo, 0, len(tm.workers))
	for _, worker := range tm.workers {
		workers = append(workers, worker)
	}
	tm.workersMu.RUnlock()

	for _, worker := range workers {
		reqID := uuid.New().String()
		checkMsg := common.NewMessage(common.TypeCheck, "", reqID)
		
		if err := tm.sendMessage(worker.Conn, checkMsg); err != nil {
			tm.logger.Errorf("Failed to send health check to worker %s: %v", worker.ID, err)
			// 送信失敗したワーカーを削除
			tm.removeWorker(worker.ID)
		} else {
			// @obj: アクティブリクエストを追加
			// @ref: SCIK9X27-000003-000016, SCIK9X27-000003-000017
			tm.addActiveRequest(worker.ID, reqID, "", common.TypeCheck)
			
			// @obj: ヘルスチェックタイムアウト監視を開始（10秒タイムアウト）
			// @ref: SCIK9X27-000003-000040
			tm.startHealthCheckTimeoutMonitoring(reqID, worker.ID, 10*time.Second)
		}
	}
}

// checkRequestTimeouts はリクエストのタイムアウトをチェックする
// @obj: 応答がないワーカーのタイムアウト処理
// @ref: SCIK9X27-000003-000019, SCIK9X27-000003-00001A
func (tm *TaskMaster) checkRequestTimeouts() {
	tm.workersMu.RLock()
	workers := make([]*WorkerInfo, 0, len(tm.workers))
	for _, worker := range tm.workers {
		workers = append(workers, worker)
	}
	tm.workersMu.RUnlock()

	// @obj: タイムアウト時間を10秒に設定
	// @ref: SCIK9X27-000001-00004C
	timeout := 10 * time.Second
	now := time.Now()

	for _, worker := range workers {
		worker.mu.Lock()
		if worker.State == StateRequesting && now.Sub(worker.RequestTime) > timeout {
			tm.logger.Warnf("Worker %s timed out on request", worker.ID)
			taskFile := worker.CurrentTask
			worker.State = StateIdle
			worker.CurrentTask = ""
			worker.RequestID = ""
			worker.mu.Unlock()

			// タスクをpendingに戻す
			if taskFile != "" {
				tm.returnTaskToPending(taskFile)
			}
		} else {
			worker.mu.Unlock()
		}
	}
}

// checkAllTasksCompleted は全タスクが完了したかチェックする
// @obj: ファイル数をチェックし、ファイル数の合計が0だった場合、COMPLETEDメッセージをメインループに送信する
// @ref: SCIK9X27-000003-00002F
func (tm *TaskMaster) checkAllTasksCompleted() {
	tm.logger.Debugf("[MASTER] checkAllTasksCompleted starting...")
	
	// @obj: .tasks/pending/と.tasks/working/以下のファイル数を数える
	// @ref: SCIK9X27-000003-00002F
	dirs := []string{"pending", "working"}
	totalFiles := 0
	
	for _, dir := range dirs {
		path := filepath.Join(tm.rootDir, ".tasks", dir)
		tm.logger.Debugf("[MASTER] Checking directory: %s", path)
		entries, err := os.ReadDir(path)
		if err != nil {
			tm.logger.Errorf("Failed to read %s directory: %v", dir, err)
			return
		}
		
		dirFiles := 0
		// ディレクトリ以外のファイルを数える
		for _, entry := range entries {
			if !entry.IsDir() {
				dirFiles++
				totalFiles++
			}
		}
		tm.logger.Debugf("[MASTER] Directory %s has %d files", dir, dirFiles)
	}

	tm.logger.Infof("[MASTER] Total files in pending+working: %d", totalFiles)

	// @obj: ファイル数の合計が0だった場合、COMPLETEDメッセージをメインループに送信
	// @ref: SCIK9X27-000003-00002F
	if totalFiles == 0 {
		tm.logger.Info("All tasks completed!")
		
		// COMPLETEDメッセージをメインループに送信
		completedMsg := &InternalMessage{
			Message:  common.NewMessage(common.TypeCompleted, "All tasks completed", ""),
			WorkerID: "TIMER_THREAD",
		}
		
		tm.logger.Debugf("[MASTER] Sending COMPLETED message to main loop...")
		select {
		case tm.msgChan <- completedMsg:
			tm.logger.Debug("Sent COMPLETED message to main loop")
		case <-time.After(1 * time.Second):
			tm.logger.Warn("Timeout sending COMPLETED message")
		case <-tm.ctx.Done():
			return
		}
	} else {
		tm.logger.Debugf("[MASTER] Still have %d tasks remaining", totalFiles)
	}
	
	tm.logger.Debugf("[MASTER] checkAllTasksCompleted finished")
}
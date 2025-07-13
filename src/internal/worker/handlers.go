package worker

import (
	"context"
	"encoding/json"
	"os/exec"
	"strings"
	"time"

	"github.com/quvox/task_organizer/internal/common"
)

// mainLoop はワーカーのメインループ
// @obj: 状態を持ちながらメッセージとイベントを処理する中央制御ループ
// @ref: SCIK9X27-000001-00003E, SCIK9X27-000001-00000F
func (tw *TaskWorker) mainLoop() {
	// @obj: 初期状態をWAITINGに設定
	// @ref: SCIK9X27-000001-000044
	tw.setState(StateWaiting)
	
	// @obj: REQUESTメッセージ受信後5秒間は、メッセージ受信チェックを高頻度（1秒間隔）で実施
	// @ref: SCIK9X27-000001-00004D
	var fastCheckTimer *time.Timer
	normalCheckInterval := 50 * time.Millisecond
	fastCheckInterval := 1 * time.Second  // 1秒間隔に変更
	checkInterval := normalCheckInterval

	ticker := time.NewTicker(checkInterval)
	defer ticker.Stop()

	for {
		select {
		case <-tw.ctx.Done():
			// @obj: ワーカー終了処理
			// @ref: SCIK9X27-000001-00001B
			tw.handleShutdown()
			return

		case msg := <-tw.msgChan:
			// @obj: マスターからのメッセージ処理
			// @ref: SCIK9X27-000001-000010, SCIK9X27-000001-000011
			tw.handleMasterMessage(msg)
			
			// REQUESTメッセージの場合、高頻度チェックを開始
			if msg.Type == common.TypeRequest {
				if fastCheckTimer != nil {
					fastCheckTimer.Stop()
				}
				// @obj: 5秒間の高頻度チェック
				// @ref: SCIK9X27-000001-00004D
				fastCheckTimer = time.AfterFunc(5*time.Second, func() {
					ticker.Reset(normalCheckInterval)
					checkInterval = normalCheckInterval
				})
				ticker.Reset(fastCheckInterval)
				checkInterval = fastCheckInterval
			}

		case <-ticker.C:
			// メッセージチェック（ノンブロッキング）
			select {
			case msg := <-tw.msgChan:
				tw.handleMasterMessage(msg)
				
				if msg.Type == common.TypeRequest && fastCheckTimer != nil {
					fastCheckTimer.Reset(5 * time.Second)
				}
			default:
				// メッセージがない場合は何もしない
			}
		}
	}
}

// handleMasterMessage はマスターからのメッセージを処理する
// @obj: メッセージタイプに応じた処理の振り分け
// @ref: SCIK9X27-000001-000010, SCIK9X27-000001-000011, SCIK9X27-000001-000012, SCIK9X27-000001-000013
func (tw *TaskWorker) handleMasterMessage(msg *common.Message) {
	switch msg.Type {
	case common.TypeCheck:
		// @obj: WAITING、WORKING状態の時にヘルスチェックメッセージ（type=CHECK）を受け取ると、即時にtype=CHECK_ACKを返答する
		// @ref: SCIK9X27-000001-000046
		if tw.getState() == StateWaiting || tw.getState() == StateWorking {
			tw.handleHealthCheck(msg)
		} else {
			// @obj: EXITING状態では、タスク管理マスタからのあらゆるメッセージの受信を破棄する
			// @ref: SCIK9X27-000001-000045
			tw.logger.Debugf("Ignoring CHECK message in %v state", tw.getState())
		}

	case common.TypeRequest:
		// @obj: WAITING状態の時にタスク実行依頼メッセージ（type=REQUEST）を受け取ると、REQUEST_ACKを返答してWORKING状態に遷移
		// @ref: SCIK9X27-000001-000047
		currentState := tw.getState()
		tw.logger.Debugf("[WORKER] Received REQUEST message in state: %v", currentState)
		if currentState == StateWaiting {
			tw.handleTaskRequest(msg)
		} else {
			// @obj: EXITING状態では、タスク管理マスタからのあらゆるメッセージの受信を破棄する
			// @ref: SCIK9X27-000001-000045
			tw.logger.Warnf("[WORKER] Ignoring REQUEST message in %v state (not WAITING)", currentState)
		}

	case common.TypeDisconn, common.TypeExit, common.TypeCompleted:
		// @obj: タスク管理マスタから、切断通知（DISCONN）を受け取ると、状態を終了処理中（EXITING）に遷移し、ワーカー終了処理を開始
		// @ref: SCIK9X27-000001-00004A
		tw.handleDisconnect()

	default:
		tw.logger.Warnf("Unknown message type from master: %s", msg.Type)
	}
}

// handleHealthCheck はヘルスチェックを処理する
// @obj: CHECK_ACKを即座に返答する。返答メッセージには、受信したリクエストIDを含める
// @ref: SCIK9X27-000001-000015
func (tw *TaskWorker) handleHealthCheck(msg *common.Message) {
	ackMsg := common.NewMessage(common.TypeCheckAck, "", msg.ReqID)
	if err := tw.sendMessage(ackMsg); err != nil {
		tw.logger.Errorf("Failed to send CHECK_ACK: %v", err)
	}
}

// handleTaskRequest はタスク実行依頼を処理する
// @obj: REQUEST_ACKを送信してWORKING状態に遷移し、AIコルーチンにタスクを渡す
// @ref: SCIK9X27-000001-000047
func (tw *TaskWorker) handleTaskRequest(msg *common.Message) {
	// @obj: 即座にREQUEST_ACKを返答し、状態をWORKING状態に遷移
	// @ref: SCIK9X27-000001-000047
	ackMsg := common.NewMessage(common.TypeRequestAck, "", msg.ReqID)
	if err := tw.sendMessage(ackMsg); err != nil {
		tw.logger.Errorf("Failed to send REQUEST_ACK: %v", err)
		return
	}
	
	// @obj: 状態をタスク実行中状態（WORKING）に遷移
	// @ref: SCIK9X27-000001-000047
	tw.setState(StateWorking)

	// @obj: AIコルーチンにタスクを渡す
	// @ref: SCIK9X27-000001-000047
	task := &AITask{
		Type:     common.TypeRequest,
		Content:  msg.Msg,
		ReqID:    msg.ReqID,
		TaskFile: msg.TaskFile, // タスクファイル名を追加
	}
	
	select {
	case tw.aiTaskChan <- task:
		tw.logger.Info("Task assigned to AI coroutine")
	default:
		tw.logger.Error("AI coroutine task channel is full")
	}
}

// handleDisconnect は切断通知を処理する
// @obj: マスターからの切断通知を受けて終了処理を開始
// @ref: SCIK9X27-000001-00004A
func (tw *TaskWorker) handleDisconnect() {
	tw.logger.Debug("Received disconnect notification from master")
	
	// @obj: 状態を終了処理中（EXITING）に遷移
	// @ref: SCIK9X27-000001-00004A
	tw.setState(StateExiting)
	
	// @obj: 実行中のタスクがあれば強制終了
	// @ref: SCIK9X27-000001-00004A
	tw.taskCancelMu.Lock()
	if tw.taskCancel != nil {
		tw.logger.Info("Forcefully terminating running AI task")
		tw.taskCancel()
	}
	tw.taskCancelMu.Unlock()
	
	// AIコルーチンに終了通知
	select {
	case tw.aiTaskChan <- &AITask{Type: common.TypeExit}:
	default:
	}
	
	tw.cancel()
}

// handleShutdown はワーカーの終了処理を実行する
// @obj: グレースフルシャットダウンの実行
// @ref: SCIK9X27-000001-00001B
func (tw *TaskWorker) handleShutdown() {
	tw.logger.Info("Starting worker shutdown...")

	// AIコルーチンに終了通知
	select {
	case tw.aiTaskChan <- &AITask{Type: common.TypeExit}:
	case <-time.After(1 * time.Second):
		tw.logger.Warn("Timeout sending exit to AI coroutine")
	}

	// 接続のクローズ
	if tw.conn != nil {
		tw.conn.Close()
	}

	tw.logger.Info("Worker shutdown complete")
}

// aiCoroutine はAIエージェントを実行するコルーチン
// @obj: AIエージェント実行コマンド（claudeコマンドなど）を実行して、その完了を待つ役割を担う
// @ref: SCIK9X27-000001-00001E, SCIK9X27-000001-00001F, SCIK9X27-000001-000020
func (tw *TaskWorker) aiCoroutine() {
	defer tw.wg.Done()
	
	// @obj: メッセージ待ち受けループは、以下のメッセージやAIエージェントからの出力を待ち受け、処理する
	// @ref: SCIK9X27-000001-000021, SCIK9X27-000001-000025
	for {
		select {
		case task := <-tw.aiTaskChan:
			switch task.Type {
			case common.TypeRequest:
				// @obj: メインループからのタスク実行依頼（REQUEST）を受け取ると、そこに書かれているプロンプトテキストをAIエージェントに与え、タスク実行を開始する（非同期）
				// @ref: SCIK9X27-000001-000022, SCIK9X27-000001-000026
				tw.wg.Add(1)
				go func(content, taskFile string) {
					defer tw.wg.Done()
					tw.executeTask(content, taskFile)
				}(task.Content, task.TaskFile)
				
			case common.TypeExit:
				// @obj: メインループからの終了メッセージ（EXIT）。待ち受けループが終了メッセージを受け取ったら、待ち受けループから脱してコルーチンを終了する
				// @ref: SCIK9X27-000001-000023, SCIK9X27-000001-00002E
				tw.logger.Info("AI coroutine received exit signal")
				return
			}
			
		case <-tw.ctx.Done():
			return
		}
	}
}

// executeTask はAIエージェントでタスクを実行する
// @obj: claudeコマンドを実行してタスクを処理し、セッションIDを引き継いで実行する
// @ref: SCIK9X27-000001-000020, SCIK9X27-000001-000024
func (tw *TaskWorker) executeTask(promptText string, taskFile string) {
	tw.logger.Info("Executing task with Claude")
	
	// @obj: セッション期限のチェックと更新
	// @ref: SCIK9X27-000000-00000C, SCIK9X27-000000-00000F
	if tw.isSessionExpired() {
		tw.logger.Warn("Session expired, attempting to refresh...")
		if err := tw.refreshSession(); err != nil {
			tw.logger.Errorf("Failed to refresh expired session: %v", err)
			// 期限切れセッションでFAILEDを送信
			resultMsg := common.NewMessage(common.TypeFailed, "Session expired", "")
			if err := tw.sendMessage(resultMsg); err != nil {
				tw.logger.Errorf("Failed to send session expired message: %v", err)
			}
			return
		}
	} else if tw.shouldRefreshSession() {
		tw.logger.Info("Session nearing expiry, refreshing proactively...")
		if err := tw.refreshSession(); err != nil {
			tw.logger.Warnf("Failed to proactively refresh session: %v", err)
			// プロアクティブ更新失敗は継続
		}
	}
	
	// @obj: claudeコマンドの構築（セッションIDとモデル名を使用）
	// @ref: SCIK9X27-000001-00004B
	var modelName string
	if tw.opus {
		modelName = "opus"
	} else {
		modelName = "sonnet"
	}
	
	cmdArgs := []string{
		"claude",
		"-r", tw.sessionID,
		"--model", modelName,
		"--allowedTools", "WebFetch,Read,Write,Bash",
		"-p", promptText,
	}

	// @obj: タスク実行用のキャンセル可能なコンテキストを作成
	// @ref: SCIK9X27-000001-00004A
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
	defer cancel()
	
	// @obj: 実行中タスクのキャンセル関数を保存（DISCONN受信時の強制終了用）
	// @ref: SCIK9X27-000001-00004A
	tw.taskCancelMu.Lock()
	tw.taskCancel = cancel
	tw.taskCancelMu.Unlock()
	
	// タスク実行完了時にキャンセル関数をクリア
	defer func() {
		tw.taskCancelMu.Lock()
		tw.taskCancel = nil
		tw.taskCancelMu.Unlock()
	}()
	
	cmd := exec.CommandContext(ctx, cmdArgs[0], cmdArgs[1:]...)
	cmd.Dir = tw.rootDir
	
	output, err := cmd.CombinedOutput()
	
	// @obj: aiコルーチンからのタスク完了通知は、そのままタスク管理マスタにタスク結果報告を送信する
	// @ref: SCIK9X27-000001-000018, SCIK9X27-000001-000019
	var resultType common.MessageType
	
	if err != nil {
		outputStr := string(output)
		
		// @obj: コンテキストキャンセル（DISCONN受信による強制終了）をチェック
		// @ref: SCIK9X27-000001-00004A
		if ctx.Err() == context.Canceled {
			tw.logger.Info("Task was forcefully terminated due to disconnect")
			resultType = common.TypeFailed
		} else if strings.Contains(outputStr, "usage") && strings.Contains(outputStr, "limit") {
			// @obj: usage limitになった場合はUSAGE_LIMITEDを送る
			// @ref: SCIK9X27-000001-00002C
			resultType = common.TypeUsageLimited
			tw.logger.Warn("Claude reached usage limit")
		} else {
			// @obj: タスク実行が何らかの理由で失敗した場合はFAILEDを送る
			// @ref: SCIK9X27-000001-00002D
			resultType = common.TypeFailed
			tw.logger.Errorf("Task execution failed: %v", err)
		}
	} else {
		// @obj: タスク実行が成功した場合はDONEを送る
		// @ref: SCIK9X27-000001-00002B
		resultType = common.TypeDone
		tw.logger.Info("Task completed successfully")
		
		// @obj: ログ出力には、AIエージェントの出力をそのまま出すのではなく、JSONオブジェクト内の、contentの中だけを出力する
		// @ref: SCIK9X27-000001-000027, SCIK9X27-000001-000028, SCIK9X27-000001-000029
		tw.logClaudeOutput(output)
	}
	
	// @obj: タスク結果報告メッセージの作成と送信
	// @ref: SCIK9X27-000001-000033, SCIK9X27-000001-000034, SCIK9X27-000001-000036
	var resultMsg *common.Message
	if resultType == common.TypeUsageLimited {
		// @obj: USAGE_LIMITEDメッセージのmsgフィールドにはワーカーIDを記載
		// @ref: SCIK9X27-000001-000036
		resultMsg = common.NewMessage(resultType, tw.workerID, "")
		tw.logger.Infof("[WORKER] ===== TASK RESULT: USAGE_LIMITED for worker %s =====", tw.workerID)
	} else {
		// @obj: DONE/FAILEDメッセージのmsgフィールドにはタスクファイル名を記載
		// @ref: SCIK9X27-000001-000033, SCIK9X27-000001-000034
		resultMsg = common.NewMessage(resultType, taskFile, "")
		tw.logger.Infof("[WORKER] ===== TASK RESULT: %s for file %s =====", resultType, taskFile)
	}
	
	tw.logger.Debugf("[WORKER] >>> SENDING RESULT MESSAGE: %s <<<", resultMsg.String())
	if err := tw.sendMessage(resultMsg); err != nil {
		tw.logger.Errorf("[WORKER] !!!!! FAILED TO SEND TASK RESULT: %v !!!!!", err)
		return // 送信失敗時は状態を変更しない
	} else {
		tw.logger.Debugf("[WORKER] +++++ SUCCESSFULLY SENT RESULT MESSAGE +++++")
	}
	
	// @obj: いずれの結果であっても、状態をタスク待受状態（WAITING）に遷移
	// @ref: SCIK9X27-000001-000048
	tw.setState(StateWaiting)
	tw.logger.Infof("[WORKER] ##### STATE CHANGED TO WAITING - READY FOR NEXT TASK #####")
}

// logClaudeOutput はClaude出力をログに記録する
// @obj: JSONレスポンスからcontentフィールドを抽出してログ出力（contentの中だけを出力）
// @ref: SCIK9X27-000001-000027
func (tw *TaskWorker) logClaudeOutput(output []byte) {
	var response struct {
		Type    string `json:"type"`
		Message struct {
			Content []struct {
				Type string `json:"type"`
				Text string `json:"text,omitempty"`
			} `json:"content"`
		} `json:"message"`
	}
	
	if err := json.Unmarshal(output, &response); err != nil {
		tw.logger.Debugf("Raw Claude output: %s", string(output))
		return
	}
	
	// contentフィールドのテキストを抽出
	for _, content := range response.Message.Content {
		if content.Type == "text" && content.Text != "" {
			tw.logger.Infof("Claude: %s", content.Text)
		}
	}
}

// setState はワーカーの状態を設定する
// @obj: スレッドセーフな状態変更
// @ref: SCIK9X27-000001-000044
func (tw *TaskWorker) setState(state WorkerState) {
	tw.stateMu.Lock()
	defer tw.stateMu.Unlock()
	tw.state = state
	tw.logger.Debugf("Worker state changed to: %v", state)
}

// getState はワーカーの現在の状態を取得する
// @obj: スレッドセーフな状態取得
// @ref: SCIK9X27-000001-000044
func (tw *TaskWorker) getState() WorkerState {
	tw.stateMu.Lock()
	defer tw.stateMu.Unlock()
	return tw.state
}
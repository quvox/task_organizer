package master

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/quvox/task_organizer/internal/common"
	"github.com/sirupsen/logrus"
)

// WorkerState はワーカーの状態を表す
// @obj: ワーカーの稼働状態管理（idle/requesting/working/disconnecting）
// @ref: SCIK9X27-000003-000015
type WorkerState int

const (
	StateIdle         WorkerState = iota // アイドル状態
	StateRequesting                      // タスク要求中
	StateWorking                         // タスク実行中
	StateDisconnecting                   // 切断処理中
)

// RequestInfo は個別リクエストの情報を保持する構造体
// @obj: リクエストIDとタイムアウト時刻の組み
// @ref: SCIK9X27-000003-000017
type RequestInfo struct {
	ID        string
	Timeout   time.Time
	TaskFile  string
	Type      common.MessageType
}

// WorkerInfo はワーカーの情報を保持する構造体
// @obj: ワーカーオブジェクトで管理する情報（ソケット、ID、ファイル名、稼働状態、リクエスト情報配列）
// @ref: SCIK9X27-000003-000011, SCIK9X27-000003-000012, SCIK9X27-000003-000013, SCIK9X27-000003-000014, SCIK9X27-000003-000015, SCIK9X27-000003-000016
type WorkerInfo struct {
	ID              string
	Conn            net.Conn
	State           WorkerState
	CurrentTask     string
	LastHealthCheck time.Time
	RequestTime     time.Time
	RequestID       string
	// @obj: 複数リクエスト情報の配列
	// @ref: SCIK9X27-000003-000016, SCIK9X27-000003-000017
	ActiveRequests  map[string]*RequestInfo
	mu              sync.Mutex
}

// TaskMaster はタスク管理マスターの主要構造体
// @obj: タスク管理マスターの機能を提供
// @ref: SCIK9X27-000003-000000, SCIK9X27-000003-000001
type TaskMaster struct {
	rootDir       string
	port          int
	logger        *logrus.Logger
	workers       map[string]*WorkerInfo
	workersMu     sync.RWMutex
	listener      net.Listener
	ctx           context.Context
	cancel        context.CancelFunc
	msgChan       chan *InternalMessage
	wg            sync.WaitGroup
	// @obj: 統計情報の追跡
	// @ref: SCIK9X27-000003-000034
	startTime     time.Time
	totalTasks    int
	successTasks  int
	failedTasks   int
	statsMu       sync.Mutex
}

// InternalMessage は内部メッセージ構造体
// @obj: ワーカーからのメッセージとその送信元を管理
// @ref: SCIK9X27-000003-00000E
type InternalMessage struct {
	Message  *common.Message
	WorkerID string
}

// NewTaskMaster は新しいTaskMasterインスタンスを作成する
// @obj: タスク管理マスターのインスタンス生成
// @ref: SCIK9X27-000003-000001, SCIK9X27-000003-000002
func NewTaskMaster(rootDir string, port int, logger *logrus.Logger) *TaskMaster {
	ctx, cancel := context.WithCancel(context.Background())
	return &TaskMaster{
		rootDir:      rootDir,
		port:         port,
		logger:       logger,
		workers:      make(map[string]*WorkerInfo),
		ctx:          ctx,
		cancel:       cancel,
		msgChan:      make(chan *InternalMessage, 100),
		startTime:    time.Now(),
		totalTasks:   0,
		successTasks: 0,
		failedTasks:  0,
	}
}

// Run はタスク管理マスターのメイン処理を実行する
// @obj: TCPサーバーの起動とメインループの実行
// @ref: SCIK9X27-000003-000003, SCIK9X27-000003-000005
func (tm *TaskMaster) Run() error {
	// @obj: TCPリスナーの起動
	// @ref: SCIK9X27-000003-000003
	listener, err := net.Listen("tcp", fmt.Sprintf(":%d", tm.port))
	if err != nil {
		return fmt.Errorf("failed to start TCP listener: %w", err)
	}
	tm.listener = listener
	tm.logger.Infof("Task master listening on port %d", tm.port)

	// @obj: タスクディレクトリの存在確認
	// @ref: SCIK9X27-000003-000006
	tasksDir := filepath.Join(tm.rootDir, ".tasks")
	if _, err := os.Stat(tasksDir); os.IsNotExist(err) {
		return fmt.Errorf(".tasks directory not found at %s", tasksDir)
	}

	// シグナルハンドラーの設定
	tm.setupSignalHandler()

	// メインループの開始
	tm.wg.Add(1)
	go tm.mainLoop()

	// @obj: タイマースレッドの開始
	// @ref: SCIK9X27-000003-000005, SCIK9X27-000003-000011
	tm.wg.Add(1)
	go tm.timerThread()

	// 接続受付ループを別のgoroutineで実行
	tm.wg.Add(1)
	go func() {
		defer tm.wg.Done()
		tm.acceptConnections()
	}()

	// シャットダウン待機
	<-tm.ctx.Done()
	
	// リスナーを閉じてAcceptブロックを解除
	if tm.listener != nil {
		tm.listener.Close()
	}
	
	tm.handleShutdown()

	// 終了待機
	tm.wg.Wait()
	return nil
}

// acceptConnections は新しい接続を受け付ける
// @obj: ワーカーからのTCP接続を受付
// @ref: SCIK9X27-000003-000004
func (tm *TaskMaster) acceptConnections() {
	tm.logger.Debug("[MASTER] Accept connections loop started")
	defer tm.logger.Debug("[MASTER] Accept connections loop stopped")
	
	for {
		select {
		case <-tm.ctx.Done():
			tm.logger.Debug("[MASTER] Context cancelled, stopping accept loop")
			return
		default:
		}
		
		conn, err := tm.listener.Accept()
		if err != nil {
			select {
			case <-tm.ctx.Done():
				tm.logger.Debug("[MASTER] Accept error during shutdown, stopping")
				return
			default:
				tm.logger.Errorf("[MASTER] Failed to accept connection: %v", err)
				continue
			}
		}

		// @obj: 新しいワーカー接続の処理
		// @ref: SCIK9X27-000003-000004
		tm.logger.Debugf("[MASTER] Accepted new connection from %s", conn.RemoteAddr())
		tm.wg.Add(1)
		go tm.handleWorkerConnection(conn)
	}
}

// handleWorkerConnection はワーカー接続を処理する
// @obj: 個別のワーカー接続を管理
// @ref: SCIK9X27-000003-000004, SCIK9X27-000003-00000E
func (tm *TaskMaster) handleWorkerConnection(conn net.Conn) {
	defer tm.wg.Done()
	defer func() {
		tm.logger.Debugf("[MASTER] Closing connection to %s", conn.RemoteAddr())
		conn.Close()
	}()
	
	tm.logger.Debugf("[MASTER] Handling worker connection from %s", conn.RemoteAddr())

	reader := bufio.NewReader(conn)
	workerID := ""

	for {
		select {
		case <-tm.ctx.Done():
			return
		default:
		}

		// @obj: メッセージの読み取り（改行区切り）
		// @ref: SCIK9X27-000006-000013
		line, err := reader.ReadString('\n')
		if err != nil {
			if err != io.EOF {
				tm.logger.Errorf("[WORKER->MASTER] Error reading from worker %s: %v", workerID, err)
			} else {
				tm.logger.Debugf("[WORKER->MASTER] Worker %s disconnected", workerID)
			}
			if workerID != "" {
				tm.removeWorker(workerID)
			}
			return
		}

		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}

		// @obj: JSONメッセージのパース
		// @ref: SCIK9X27-000006-000000
		tm.logger.Debugf("[WORKER->MASTER] Received raw message: %s", strings.TrimSpace(line))
		var msg common.Message
		if err := json.Unmarshal([]byte(line), &msg); err != nil {
			tm.logger.Errorf("[WORKER->MASTER] Failed to parse message from worker: %v", err)
			continue
		}
		tm.logger.Debugf("[WORKER->MASTER] Parsed message: %s", msg.String())

		// @obj: JOINメッセージの特別処理
		// @ref: SCIK9X27-000003-000007, SCIK9X27-000003-000008
		if msg.Type == common.TypeJoin {
			workerID = msg.Msg
			tm.addWorker(workerID, conn)
			
			// JOIN_ACKの送信
			ackMsg := common.NewMessage(common.TypeJoinAck, "", "")
			if err := tm.sendMessage(conn, ackMsg); err != nil {
				tm.logger.Errorf("Failed to send JOIN_ACK to worker %s: %v", workerID, err)
				return
			}
			tm.logger.Infof("Worker %s joined", workerID)
			continue
		}

		// @obj: その他のメッセージをメインループに転送
		// @ref: SCIK9X27-000003-00000E
		if workerID != "" {
			tm.logger.Debugf("[MASTER] @@@@@ QUEUING MESSAGE TO MAIN LOOP: Type=%s, WorkerID=%s, ChannelLen=%d @@@@@", msg.Type, workerID, len(tm.msgChan))
			tm.msgChan <- &InternalMessage{
				Message:  &msg,
				WorkerID: workerID,
			}
			tm.logger.Debugf("[MASTER] @@@@@ MESSAGE QUEUED SUCCESSFULLY, ChannelLen=%d @@@@@", len(tm.msgChan))
		} else {
			tm.logger.Warnf("[MASTER] !!!!! DROPPING MESSAGE FROM UNKNOWN WORKER: %s !!!!!", msg.String())
		}
	}
}

// sendMessage はワーカーにメッセージを送信する
// @obj: TCP接続を通じてJSONメッセージを送信
// @ref: SCIK9X27-000006-000013
func (tm *TaskMaster) sendMessage(conn net.Conn, msg *common.Message) error {
	tm.logger.Debugf("[MASTER->WORKER] Sending message: %s", msg.String())
	data, err := msg.Marshal()
	if err != nil {
		tm.logger.Errorf("[MASTER->WORKER] Failed to marshal message: %v", err)
		return err
	}
	_, err = conn.Write(data)
	if err != nil {
		tm.logger.Errorf("[MASTER->WORKER] Failed to write message: %v", err)
	} else {
		tm.logger.Debugf("[MASTER->WORKER] Message sent successfully: %s", msg.Type)
	}
	return err
}

// addWorker はワーカーを登録する
// @obj: 新しいワーカーの情報を管理マップに追加
// @ref: SCIK9X27-000003-000007
func (tm *TaskMaster) addWorker(workerID string, conn net.Conn) {
	tm.workersMu.Lock()
	defer tm.workersMu.Unlock()

	tm.workers[workerID] = &WorkerInfo{
		ID:              workerID,
		Conn:            conn,
		State:           StateIdle,
		LastHealthCheck: time.Now(),
		ActiveRequests:  make(map[string]*RequestInfo),
	}
}

// removeWorker はワーカーを削除する
// @obj: 切断したワーカーの情報を削除
// @ref: SCIK9X27-000003-00001C
func (tm *TaskMaster) removeWorker(workerID string) {
	tm.workersMu.Lock()
	defer tm.workersMu.Unlock()

	if worker, exists := tm.workers[workerID]; exists {
		// @obj: 実行中のタスクを.tasks/pending/に戻す
		// @ref: SCIK9X27-000003-00001B
		if worker.CurrentTask != "" && worker.State == StateWorking {
			tm.returnTaskToPending(worker.CurrentTask)
		}
		delete(tm.workers, workerID)
		tm.logger.Infof("Worker %s removed", workerID)
	}
}

// returnTaskToPending はタスクをpendingディレクトリに戻す
// @obj: 失敗または中断されたタスクを再実行可能にする
// @ref: SCIK9X27-000003-00001B
func (tm *TaskMaster) returnTaskToPending(taskFile string) {
	workingPath := filepath.Join(tm.rootDir, ".tasks", "working", taskFile)
	pendingPath := filepath.Join(tm.rootDir, ".tasks", "pending", taskFile)
	
	if err := os.Rename(workingPath, pendingPath); err != nil {
		tm.logger.Errorf("Failed to return task %s to pending: %v", taskFile, err)
	} else {
		tm.logger.Infof("Returned task %s to pending", taskFile)
	}
}

// incrementTotalTasks は全タスク数をインクリメントする
// @obj: 統計追跡のための全タスク数カウンタの更新
// @ref: SCIK9X27-000003-000034
func (tm *TaskMaster) incrementTotalTasks() {
	tm.statsMu.Lock()
	defer tm.statsMu.Unlock()
	tm.totalTasks++
}

// incrementSuccessTasks は成功タスク数をインクリメントする
// @obj: 統計追跡のための成功タスク数カウンタの更新
// @ref: SCIK9X27-000003-000034
func (tm *TaskMaster) incrementSuccessTasks() {
	tm.statsMu.Lock()
	defer tm.statsMu.Unlock()
	tm.successTasks++
}

// incrementFailedTasks は失敗タスク数をインクリメントする
// @obj: 統計追跡のための失敗タスク数カウンタの更新
// @ref: SCIK9X27-000003-000034
func (tm *TaskMaster) incrementFailedTasks() {
	tm.statsMu.Lock()
	defer tm.statsMu.Unlock()
	tm.failedTasks++
}

// getStatistics は統計情報を取得する
// @obj: 現在の統計情報のスナップショットを取得
// @ref: SCIK9X27-000003-000034
func (tm *TaskMaster) getStatistics() (time.Duration, int, int, int) {
	tm.statsMu.Lock()
	defer tm.statsMu.Unlock()
	duration := time.Since(tm.startTime)
	return duration, tm.totalTasks, tm.successTasks, tm.failedTasks
}

// addActiveRequest はワーカーのアクティブリクエストを追加する
// @obj: ワーカーの複数リクエスト追跡に新しいリクエストを追加
// @ref: SCIK9X27-000003-000016, SCIK9X27-000003-000017
func (tm *TaskMaster) addActiveRequest(workerID string, requestID string, taskFile string, msgType common.MessageType) {
	tm.workersMu.RLock()
	worker, exists := tm.workers[workerID]
	tm.workersMu.RUnlock()
	
	if exists {
		worker.mu.Lock()
		worker.ActiveRequests[requestID] = &RequestInfo{
			ID:       requestID,
			TaskFile: taskFile,
			Timeout:  time.Now().Add(10 * time.Second),
			Type:     msgType,
		}
		worker.mu.Unlock()
	}
}

// removeActiveRequest はワーカーのアクティブリクエストを削除する
// @obj: 完了または失敗したリクエストを追跡から削除
// @ref: SCIK9X27-000003-000016, SCIK9X27-000003-000017
func (tm *TaskMaster) removeActiveRequest(workerID string, requestID string) *RequestInfo {
	tm.workersMu.RLock()
	worker, exists := tm.workers[workerID]
	tm.workersMu.RUnlock()
	
	if exists {
		worker.mu.Lock()
		defer worker.mu.Unlock()
		
		if requestInfo, exists := worker.ActiveRequests[requestID]; exists {
			delete(worker.ActiveRequests, requestID)
			return requestInfo
		}
	}
	return nil
}

// findActiveRequest はリクエストIDから該当するワーカーとリクエスト情報を検索する
// @obj: 全ワーカーからリクエストIDで検索
// @ref: SCIK9X27-000003-000016, SCIK9X27-000003-000017
func (tm *TaskMaster) findActiveRequest(requestID string) (*WorkerInfo, *RequestInfo) {
	tm.workersMu.RLock()
	defer tm.workersMu.RUnlock()
	
	for _, worker := range tm.workers {
		worker.mu.Lock()
		if requestInfo, exists := worker.ActiveRequests[requestID]; exists {
			worker.mu.Unlock()
			return worker, requestInfo
		}
		worker.mu.Unlock()
	}
	return nil, nil
}
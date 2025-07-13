package worker

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"os/signal"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/quvox/task_organizer/internal/common"
	"github.com/sirupsen/logrus"
)

// WorkerState はワーカーの状態を表す
// @obj: メインループの状態管理
// @ref: SCIK9X27-000001-000044
type WorkerState int

const (
	// @obj: タスク待受状態
	// @ref: SCIK9X27-000001-000044
	StateWaiting WorkerState = iota
	// @obj: タスク実行中状態
	// @ref: SCIK9X27-000001-000044
	StateWorking
	// @obj: 終了処理中状態
	// @ref: SCIK9X27-000001-000044
	StateExiting
)


// TaskWorker はタスクワーカーの主要構造体
// @obj: タスクワーカーの機能を提供
// @ref: SCIK9X27-000001-000000, SCIK9X27-000001-000004
type TaskWorker struct {
	masterHost    string
	masterPort    int
	rootDir       string
	opus          bool
	sessionID     string
	workerID      string
	logger        *logrus.Logger
	conn          net.Conn
	ctx           context.Context
	cancel        context.CancelFunc
	msgChan       chan *common.Message
	aiTaskChan    chan *AITask
	wg            sync.WaitGroup
	// @obj: セッション管理
	// @ref: SCIK9X27-000000-00000C, SCIK9X27-000000-00000F
	sessionExpiry time.Time
	sessionMu     sync.Mutex
	// @obj: ワーカー状態管理
	// @ref: SCIK9X27-000001-000044
	state         WorkerState
	stateMu       sync.Mutex
	// @obj: 実行中のタスクの強制終了用
	// @ref: SCIK9X27-000001-00004A
	taskCancel    context.CancelFunc
	taskCancelMu  sync.Mutex
}

// AITask はAIコルーチンへのタスク
// @obj: AIエージェントに渡すタスク情報
// @ref: SCIK9X27-000001-00001E, SCIK9X27-000001-000021
type AITask struct {
	Type     common.MessageType
	Content  string
	ReqID    string
	TaskFile string // タスクファイル名を追加
}


// NewTaskWorker は新しいTaskWorkerインスタンスを作成する
// @obj: タスクワーカーのインスタンス生成
// @ref: SCIK9X27-000001-000001, SCIK9X27-000001-000002, SCIK9X27-000001-000003
func NewTaskWorker(masterHost string, masterPort int, rootDir string, opus bool, logger *logrus.Logger) *TaskWorker {
	ctx, cancel := context.WithCancel(context.Background())
	return &TaskWorker{
		masterHost:    masterHost,
		masterPort:    masterPort,
		rootDir:       rootDir,
		opus:          opus,
		logger:        logger,
		ctx:           ctx,
		cancel:        cancel,
		msgChan:       make(chan *common.Message, 100),
		aiTaskChan:    make(chan *AITask, 10),
		sessionExpiry: time.Now().Add(24 * time.Hour), // デフォルト24時間
	}
}

// Run はタスクワーカーのメイン処理を実行する
// @obj: ワーカーの準備処理とメインループの実行
// @ref: SCIK9X27-000001-000009, SCIK9X27-000001-00000A, SCIK9X27-000001-00000B
func (tw *TaskWorker) Run() error {
	// @obj: 準備処理の実施
	// @ref: SCIK9X27-000001-000009
	if err := tw.initialize(); err != nil {
		return err
	}

	// シグナルハンドラーの設定
	tw.setupSignalHandler()

	// @obj: タスク管理マスタとのTCPコネクション確立
	// @ref: SCIK9X27-000001-00000B
	conn, err := net.Dial("tcp", fmt.Sprintf("%s:%d", tw.masterHost, tw.masterPort))
	if err != nil {
		return fmt.Errorf("failed to connect to master: %w", err)
	}
	tw.conn = conn
	defer tw.conn.Close()

	// ソケットオプションの設定
	if tcpConn, ok := conn.(*net.TCPConn); ok {
		// @obj: TCP_NODELAYを設定して即座にメッセージを送信
		// @ref: SCIK9X27-000001-00003E
		tcpConn.SetNoDelay(true)
		// @obj: ソケット受信バッファを大きく設定
		// @ref: SCIK9X27-000001-00003D
		tcpConn.SetReadBuffer(65536)
	}

	// @obj: ワーカーIDの生成（ホスト名+プロセスID）
	// @ref: SCIK9X27-000001-000043
	hostname, _ := os.Hostname()
	workerID := fmt.Sprintf("%s-%d", hostname, os.Getpid())
	tw.workerID = workerID
	
	// @obj: 参入メッセージの送信
	// @ref: SCIK9X27-000001-00000C, SCIK9X27-000001-000043
	joinMsg := common.NewMessage(common.TypeJoin, workerID, "")
	if err := tw.sendMessage(joinMsg); err != nil {
		return fmt.Errorf("failed to send JOIN message: %w", err)
	}

	// @obj: 参入応答メッセージの受信待ち
	// @ref: SCIK9X27-000001-00000E
	if err := tw.waitForJoinAck(); err != nil {
		return err
	}

	tw.logger.Infof("Successfully joined master as worker %s", workerID)

	// AIコルーチンの起動
	tw.wg.Add(1)
	go tw.aiCoroutine()

	// メッセージ受信コルーチンの起動
	tw.wg.Add(1)
	go tw.receiveMessages()

	// @obj: メインループの実行
	// @ref: SCIK9X27-000001-00000F
	tw.mainLoop()

	// 終了待機
	tw.wg.Wait()
	return nil
}

// initialize は初期化処理を実行する
// @obj: Claude セッションID取得を含む準備処理
// @ref: SCIK9X27-000001-000009, SCIK9X27-000001-00003F
func (tw *TaskWorker) initialize() error {
	// @obj: Claude セッションIDの取得
	// @ref: SCIK9X27-000001-000005, SCIK9X27-000001-000040, SCIK9X27-000001-000041, SCIK9X27-000001-000042
	sessionID, err := tw.createClaudeSession()
	if err != nil {
		// @obj: ログインできなかった場合など、正常に利用できない場合はプログラムを終了
		// @ref: SCIK9X27-000001-00003F
		tw.logger.Errorf("Failed to create Claude session: %v", err)
		return fmt.Errorf("failed to create Claude session: %w", err)
	}
	tw.sessionID = sessionID
	
	// @obj: 取得したセッションIDをINFOメッセージとして出力
	// @ref: SCIK9X27-000001-000042
	tw.logger.Infof("Claude session ID: %s", sessionID)
	
	return nil
}


// createClaudeSession はClaude セッションを作成する
// @obj: claudeを起動してセッションIDを取得
// @ref: SCIK9X27-000000-000004, SCIK9X27-000000-000006, SCIK9X27-000001-000005
func (tw *TaskWorker) createClaudeSession() (string, error) {
	// @obj: モデル名の設定（ツールのオプショナルに--opusが指定されている場合には、＜モデル名＞に"opus"を指定し、そうでなければ、"sonnet"を指定する）
	// @ref: SCIK9X27-000001-000041
	var modelName string
	if tw.opus {
		modelName = "opus"
	} else {
		modelName = "sonnet"
	}
	
	// @obj: セッション初期化用のコマンド引数設定（統一形式）
	// @ref: SCIK9X27-000001-000040
	cmdArgs := []string{"claude", "-r", tw.sessionID, "--model", modelName, "--allowedTools", "WebFetch,Read,Write,Bash", "-p", ".", "--output-format", "json"}
	
	// 初回セッション作成時は-rオプションを使わない
	if tw.sessionID == "" {
		cmdArgs = []string{"claude", "--model", modelName, "--allowedTools", "WebFetch,Read,Write,Bash", "-p", ".", "--output-format", "json"}
	}

	// コマンドの実行（ルートディレクトリで実行）
	cmd := exec.Command(cmdArgs[0], cmdArgs[1:]...)
	cmd.Dir = tw.rootDir
	
	output, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("Claude CLI failed: %w", err)
	}

	// @obj: JSONレスポンスからセッションIDを抽出
	// @ref: SCIK9X27-000000-000006
	var response struct {
		SessionID string `json:"session_id"`
		IsError   bool   `json:"is_error"`
	}
	
	if err := json.Unmarshal(output, &response); err != nil {
		return "", fmt.Errorf("failed to parse Claude response: %w", err)
	}
	
	if response.IsError {
		return "", fmt.Errorf("Claude returned error")
	}
	
	if response.SessionID == "" {
		return "", fmt.Errorf("no session ID in Claude response")
	}
	
	return response.SessionID, nil
}

// setupSignalHandler はシグナルハンドラーを設定する
// @obj: Ctrl-Cなどのシグナルを処理。二度目のシグナルで強制終了
// @ref: SCIK9X27-000001-00001D, SCIK9X27-000001-00001C
func (tw *TaskWorker) setupSignalHandler() {
	sigChan := make(chan os.Signal, 2) // バッファサイズを増やして二度目のシグナルを受け取る
	signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM)

	go func() {
		// 初回のシグナル
		<-sigChan
		tw.logger.Info("Received first shutdown signal, starting graceful shutdown...")
		
		// @obj: 実行中のタスクがある場合は完了を待つ
		// @ref: SCIK9X27-000001-00001D
		currentState := tw.getState()
		if currentState == StateWorking {
			tw.logger.Info("Task is running, waiting for completion before shutdown...")
			
			// AIコルーチンに終了通知（タスクの完了を待つために即座には接続を閉じない）
			select {
			case tw.aiTaskChan <- &AITask{Type: common.TypeExit}:
			default:
			}
			
			// タスクが完了するまで待機（最大30秒）
			for i := 0; i < 300; i++ { // 30秒 = 300 * 100ms
				if tw.getState() != StateWorking {
					tw.logger.Info("Task completed, proceeding with shutdown")
					break
				}
				time.Sleep(100 * time.Millisecond)
				if i == 299 {
					tw.logger.Warn("Task did not complete within 30 seconds, forcing shutdown")
				}
			}
		} else {
			// @obj: AIコルーチンに終了通知
			// @ref: SCIK9X27-000001-00001D
			select {
			case tw.aiTaskChan <- &AITask{Type: common.TypeExit}:
			default:
			}
		}
		
		// @obj: 切断通知の送信（タスク完了後）
		// @ref: SCIK9X27-000001-00001D
		if tw.conn != nil {
			leaveMsg := common.NewMessage(common.TypeLeave, "", "")
			tw.sendMessage(leaveMsg)
		}
		
		tw.cancel()
		
		// 二度目のシグナル監視開始
		go func() {
			<-sigChan
			// @obj: ワーカー終了処理中にCtrl-C（SIG_TERM）が発生したら、aiコルーチンがあればそれを強制停止して、即座にプログラムを終了する
			// @ref: SCIK9X27-000001-00001C
			tw.logger.Warn("Received second shutdown signal, forcing immediate exit")
			os.Exit(1)
		}()
	}()
}

// sendMessage はメッセージを送信する
// @obj: マスターへのメッセージ送信
// @ref: SCIK9X27-000001-00002F, SCIK9X27-000006-000013
func (tw *TaskWorker) sendMessage(msg *common.Message) error {
	tw.logger.Debugf("[WORKER->MASTER] Sending message: %s", msg.String())
	data, err := msg.Marshal()
	if err != nil {
		tw.logger.Errorf("[WORKER->MASTER] Failed to marshal message: %v", err)
		return err
	}
	tw.logger.Infof("[WORKER->MASTER] Marshaled data: %s", string(data))
	_, err = tw.conn.Write(data)
	if err != nil {
		tw.logger.Errorf("[WORKER->MASTER] Failed to write message: %v", err)
	} else {
		tw.logger.Infof("[WORKER->MASTER] Message sent successfully: %s", msg.Type)
	}
	return err
}

// waitForJoinAck は参入応答を待つ
// @obj: JOIN_ACKメッセージの受信を待機
// @ref: SCIK9X27-000001-00000E
func (tw *TaskWorker) waitForJoinAck() error {
	reader := bufio.NewReader(tw.conn)
	line, err := reader.ReadString('\n')
	if err != nil {
		return fmt.Errorf("failed to read JOIN_ACK: %w", err)
	}

	var msg common.Message
	if err := json.Unmarshal([]byte(strings.TrimSpace(line)), &msg); err != nil {
		return fmt.Errorf("failed to parse JOIN_ACK: %w", err)
	}

	if msg.Type != common.TypeJoinAck {
		return fmt.Errorf("expected JOIN_ACK, got %s", msg.Type)
	}

	return nil
}

// receiveMessages はマスターからのメッセージを受信する
// @obj: TCP接続からメッセージを継続的に受信
// @ref: SCIK9X27-000001-000014, SCIK9X27-000001-00000C
func (tw *TaskWorker) receiveMessages() {
	defer tw.wg.Done()
	reader := bufio.NewReader(tw.conn)

	for {
		select {
		case <-tw.ctx.Done():
			return
		default:
		}

		// @obj: メッセージの読み取り（改行区切り）
		// @ref: SCIK9X27-000006-000013
		line, err := reader.ReadString('\n')
		if err != nil {
			if err != io.EOF {
				tw.logger.Errorf("[MASTER->WORKER] Error reading from master: %v", err)
			} else {
				tw.logger.Debug("[MASTER->WORKER] Connection closed by master")
			}
			tw.cancel()
			return
		}

		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}

		// @obj: JSONメッセージのパース
		// @ref: SCIK9X27-000006-000000
		tw.logger.Debugf("[MASTER->WORKER] Received raw message: %s", strings.TrimSpace(line))
		var msg common.Message
		if err := json.Unmarshal([]byte(line), &msg); err != nil {
			tw.logger.Errorf("[MASTER->WORKER] Failed to parse message: %v", err)
			continue
		}
		tw.logger.Debugf("[MASTER->WORKER] Parsed message: %s", msg.String())

		// メッセージをチャネルに送信
		select {
		case tw.msgChan <- &msg:
		case <-tw.ctx.Done():
			return
		}
	}
}

// getLocalPort はローカルポート番号を取得する
// @obj: ワーカーIDとして使用するTCPポート番号を取得
// @ref: SCIK9X27-000001-00000D
func getLocalPort(conn net.Conn) int {
	if addr, ok := conn.LocalAddr().(*net.TCPAddr); ok {
		return addr.Port
	}
	return 0
}

// isSessionExpired はセッションが期限切れかチェックする
// @obj: セッション有効期限の確認
// @ref: SCIK9X27-000000-00000C, SCIK9X27-000000-00000F
func (tw *TaskWorker) isSessionExpired() bool {
	tw.sessionMu.Lock()
	defer tw.sessionMu.Unlock()
	return time.Now().After(tw.sessionExpiry)
}

// refreshSession はセッションを更新する
// @obj: セッション期限が近づいた際の自動更新
// @ref: SCIK9X27-000000-00000C, SCIK9X27-000000-00000F
func (tw *TaskWorker) refreshSession() error {
	tw.logger.Info("Refreshing Claude session")
	
	newSessionID, err := tw.createClaudeSession()
	if err != nil {
		return fmt.Errorf("failed to refresh session: %w", err)
	}
	
	tw.sessionMu.Lock()
	tw.sessionID = newSessionID
	tw.sessionExpiry = time.Now().Add(23 * time.Hour) // 23時間後に期限設定（余裕を持たせる）
	tw.sessionMu.Unlock()
	
	tw.logger.Infof("Session refreshed: %s", newSessionID)
	return nil
}

// shouldRefreshSession はセッション更新が必要かチェックする
// @obj: セッション更新のタイミング判定（期限1時間前）
// @ref: SCIK9X27-000000-00000C, SCIK9X27-000000-00000F
func (tw *TaskWorker) shouldRefreshSession() bool {
	tw.sessionMu.Lock()
	defer tw.sessionMu.Unlock()
	// 期限の1時間前に更新する
	return time.Now().After(tw.sessionExpiry.Add(-1 * time.Hour))
}
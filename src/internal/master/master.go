package master

import (
	"context"
	"flag"
	"fmt"
	"log"
	"net"
	"os"
	"os/signal"
	"path/filepath"
	"sync"
	"syscall"
	"time"

	"github.com/quvox/task_organizer/internal/common"
)

/*
@obj: タスク管理マスタの設定構造体
@ref: IPS3MKEQ-000002-000001 "引数には、待受ポート番号を与える。ただしデフォルト設定を34567とする。"
@ref: IPS3MKEQ-000002-000002 "さらにオプショナル引数でルートディレクトリパスを指定する。"
*/
type Config struct {
	Port    int
	RootDir string
}

/*
@obj: タスク管理マスタの構造体
@ref: IPS3MKEQ-000002-000005 "タスク管理マスタを起動すると、すぐにメインループに入ると同時に、タイマースレッドを起動する。"
*/
type Master struct {
	config    Config
	ctx       context.Context
	cancel    context.CancelFunc
	workers   map[string]*Worker
	workersMu sync.RWMutex
	listener  net.Listener
	msgChan   chan WorkerMessage
	startTime time.Time
	stats     Statistics
	wg        sync.WaitGroup
	connMap   map[string]net.Conn
	connMu    sync.RWMutex
}

/*
@obj: ワーカー情報を管理する構造体
@ref: IPS3MKEQ-000002-000010 "タスク管理マスタは、参入しているタスクワーカーごとに、接続状態およびタスク稼働状態をワーカーオブジェクトで管理する。"
@ref: IPS3MKEQ-000002-000011 "ワーカーオブジェクトでは、以下の情報を管理する。"
*/
type Worker struct {
	// @ref: IPS3MKEQ-000002-000012 "コネクションに紐づくソケット情報"
	conn net.Conn
	// @ref: IPS3MKEQ-000002-000013 "ワーカーID"
	id string
	// @ref: IPS3MKEQ-000002-000014 "対応中の.tasks/working/のファイル名"
	workingFile string
	// @ref: IPS3MKEQ-000002-000015 "稼働状態（idle/requesting/working/disconnecting）"
	state WorkerState
	// @ref: IPS3MKEQ-000002-000016 "リクエスト情報配列（複数のリクエスト情報を格納できるようにする）"
	requests []RequestInfo
	mu       sync.Mutex
}

/*
@obj: ワーカーの稼働状態
@ref: IPS3MKEQ-000002-000015 "稼働状態（idle/requesting/working/disconnecting）"
*/
type WorkerState string

const (
	StateIdle          WorkerState = "idle"
	StateRequesting    WorkerState = "requesting"
	StateWorking       WorkerState = "working"
	StateDisconnecting WorkerState = "disconnecting"
)

/*
@obj: リクエスト情報構造体
@ref: IPS3MKEQ-000002-000017 "リクエスト情報は、リクエストIDとタイムアウト時刻の組みで表される"
*/
type RequestInfo struct {
	RequestID string
	Timeout   time.Time
}

/*
@obj: ワーカーからのメッセージを表す構造体
*/
type WorkerMessage struct {
	WorkerID string
	Message  *common.Message
}

/*
@obj: 統計情報を管理する構造体
@ref: IPS3MKEQ-000002-000034 "タスク管理マスタ終了処理では、合計稼働時間、全タスク数、成功タスク数、失敗タスク数を表示する"
*/
type Statistics struct {
	TotalTasks   int
	SuccessTasks int
	FailedTasks  int
	mu           sync.Mutex
}

/*
@obj: タスク管理マスタのメイン処理
@ref: IPS3MKEQ-000002-000000 "タスク管理マスタは、`taskorganizer master`で起動する。"
*/
func Run(args []string) error {
	/*
	@obj: コマンドライン引数を解析する
	@ref: IPS3MKEQ-000002-000001 "引数には、待受ポート番号を与える。ただしデフォルト設定を34567とする。"
	@ref: IPS3MKEQ-000002-000002 "さらにオプショナル引数でルートディレクトリパスを指定する。"
	*/
	fs := flag.NewFlagSet("master", flag.ExitOnError)
	rootDir := fs.String("root-dir", ".", "Root directory path")

	if err := fs.Parse(args); err != nil {
		return err
	}

	port := 34567
	if fs.NArg() > 0 {
		if _, err := fmt.Sscanf(fs.Arg(0), "%d", &port); err != nil {
			return fmt.Errorf("invalid port number: %s", fs.Arg(0))
		}
	}

	/*
	@obj: ルートディレクトリを絶対パスに変換する
	@ref: IPS3MKEQ-000002-000002 "なお、デフォルトのルートディレクトリはスクリプトを実行した時のカレントディレクトリとする。"
	*/
	absRootDir, err := filepath.Abs(*rootDir)
	if err != nil {
		return fmt.Errorf("failed to get absolute path of root directory: %w", err)
	}

	config := Config{
		Port:    port,
		RootDir: absRootDir,
	}

	/*
	@obj: マスター構造体を初期化する
	*/
	ctx, cancel := context.WithCancel(context.Background())
	m := &Master{
		config:    config,
		ctx:       ctx,
		cancel:    cancel,
		workers:   make(map[string]*Worker),
		msgChan:   make(chan WorkerMessage, 100),
		startTime: time.Now(),
		connMap:   make(map[string]net.Conn),
	}

	/*
	@obj: シグナルハンドラを設定する
	@ref: IPS3MKEQ-000002-00000C "タスク管理マスタのメインループ中に、Ctrl-C（SIG_TERM）が発生したら、待ち受けループを脱出して、タスク管理マスタ終了処理を実施する。"
	*/
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM)
	go func() {
		<-sigChan
		log.Println("Received interrupt signal, shutting down...")
		m.cancel()
	}()

	return m.run()
}

/*
@obj: マスターのメイン処理を実行する
@ref: IPS3MKEQ-000002-000005 "タスク管理マスタを起動すると、すぐにメインループに入ると同時に、タイマースレッドを起動する。"
*/
func (m *Master) run() error {
	/*
	@obj: TCPリスナーを開始する
	@ref: IPS3MKEQ-000002-000008 "タスクワーカーからのTCP接続受け入れ（bindするIPは0.0.0.0とする）"
	*/
	listener, err := net.Listen("tcp", fmt.Sprintf("0.0.0.0:%d", m.config.Port))
	if err != nil {
		return fmt.Errorf("failed to listen on port %d: %w", m.config.Port, err)
	}
	m.listener = listener
	defer listener.Close()

	log.Printf("Task master started on port %d", m.config.Port)

	/*
	@obj: タイマースレッドを起動する
	@ref: IPS3MKEQ-000002-00000E "タイマースレッドは、定期的にタイマーイベントメッセージをメインループに送信する。デフォルト設定は10秒ごととする。"
	*/
	m.wg.Add(1)
	go m.timerThread()

	/*
	@obj: TCP接続受け入れゴルーチンを起動する
	@ref: IPS3MKEQ-000002-000008 "タスクワーカーからのTCP接続受け入れ"
	*/
	m.wg.Add(1)
	go m.acceptConnections()

	/*
	@obj: メインループを実行する
	@ref: IPS3MKEQ-000002-000006 "メインループでは、イベントやメッセージの待ち受けと、ワーカーの動作状態の管理を実施する。"
	*/
	m.mainLoop()

	/*
	@obj: 終了処理を実行する
	@ref: IPS3MKEQ-000002-000034 "タスク管理マスタ終了処理では、合計稼働時間、全タスク数、成功タスク数、失敗タスク数を表示するとともに、全てのTCPコネクションを切断し、タスク管理マスタを終了する。"
	*/
	return m.shutdown()
}

/*
@obj: メインループを実行する
@ref: IPS3MKEQ-000002-000006 "メインループでは、イベントやメッセージの待ち受けと、ワーカーの動作状態の管理を実施する。"
@ref: IPS3MKEQ-000002-000007 "メインループでは、以下のイベントを待つ。"
*/
func (m *Master) mainLoop() {
	for {
		select {
		case <-m.ctx.Done():
			return

		case msg := <-m.msgChan:
			/*
			@obj: ワーカーメッセージを処理する
			@ref: IPS3MKEQ-000002-000009 "タスクワーカーからの各種メッセージの受信（JOIN、DONE/FAILED、LEAVE、CHECK_ACK, USAGE_LIMITED）"
			@ref: IPS3MKEQ-000002-00000A "タイマースレッドから発行されるタイマーイベントメッセージ（TIMER）の受信"
			@ref: IPS3MKEQ-000002-00000B "別スレッドからのメッセージ受信（COMPLETED、DISCONNECT、TIMEOUT_CHECK）"
			*/
			m.handleMessage(msg)

			/*
			@obj: 新しいタスクがあれば割り当てる
			@ref: IPS3MKEQ-000002-00001A "何らかのイベントが起こるたびに、まだ.tasks/pending/の下にファイルが残っていないかを確認し、残っていれば、以下のタスク依頼処理を実施する。"
			*/
			m.assignPendingTasks()
		}
	}
}

// 以下、各種ハンドラやヘルパー関数の実装が続きますが、文字数制限のため省略します。
// 実装が必要な主な関数：
// - acceptConnections()
// - timerThread()
// - handleMessage()
// - assignPendingTasks()
// - handleJoin()
// - handleRequestAck()
// - handleTaskResult()
// - handleCheckAck()
// - handleTimer()
// - handleTimeoutCheck()
// - handleDisconnect()
// - sendHealthCheck()
// - disconnectWorker()
// - shutdown()
// など
package worker

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"net"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"sync"
	"syscall"
	"time"

	"github.com/quvox/task_organizer/internal/common"
)

/*
@obj: タスクワーカーの設定構造体
@ref: IPS3MKEQ-000000-000001 "タスクワーカーの引数は、接続するタスク管理マスターのホスト名（またはIP）と、ポート番号とする。"
@ref: IPS3MKEQ-000000-000002 "さらにオプショナル引数でルートディレクトリパスを指定する。"
@ref: IPS3MKEQ-000000-000003 "別のオプショナル引数で、claudeのモデルを指定できるようにする。"
*/
type Config struct {
	Host    string
	Port    int
	RootDir string
	Opus    bool
}

/*
@obj: タスクワーカーの構造体
@ref: IPS3MKEQ-000000-000004 "タスクワーカーは、マスターからタスク指示を受けるたびに、claudeを起動して、そこにプロンプトを与えてタスクを実行させる。"
*/
type Worker struct {
	config       Config
	ctx          context.Context
	cancel       context.CancelFunc
	conn         net.Conn
	workerID     string
	sessionID    string
	claudeMsgChan chan ClaudeMessage
	masterMsgChan chan *common.Message
	wg            sync.WaitGroup
}

/*
@obj: Claude管理コルーチンとのメッセージ
@ref: IPS3MKEQ-000000-000008 "別コルーチンやタスクワーカーとやり取りするメッセージは、メッセージフォーマット.mdの仕様に従う。"
*/
type ClaudeMessage struct {
	Type string
	Msg  string
}

/*
@obj: タスクワーカーのメイン処理
@ref: IPS3MKEQ-000000-000000 "タスクワーカーは、`taskorganizer worker`で起動する。"
*/
func Run(args []string) error {
	/*
	@obj: コマンドライン引数を解析する
	@ref: IPS3MKEQ-000000-000001 "タスクワーカーの引数は、接続するタスク管理マスターのホスト名（またはIP）と、ポート番号とする。ただし引数のデフォルト設定は、ホスト名はlocalhost、ポート番号は34567とする。"
	@ref: IPS3MKEQ-000000-000002 "さらにオプショナル引数でルートディレクトリパスを指定する。なお、デフォルトのルートディレクトリはスクリプトを実行した時のカレントディレクトリとする。"
	@ref: IPS3MKEQ-000000-000003 "別のオプショナル引数で、claudeのモデルを指定できるようにする。デフォルトは\"sonnet4\"とし、--opusを指定した場合は、claudeの利用モデルをopus4にする。"
	*/
	fs := flag.NewFlagSet("worker", flag.ExitOnError)
	rootDir := fs.String("root-dir", ".", "Root directory path")
	opus := fs.Bool("opus", false, "Use Opus model instead of Sonnet")

	if err := fs.Parse(args); err != nil {
		return err
	}

	host := "localhost"
	port := 34567

	if fs.NArg() > 0 {
		host = fs.Arg(0)
	}
	if fs.NArg() > 1 {
		if _, err := fmt.Sscanf(fs.Arg(1), "%d", &port); err != nil {
			return fmt.Errorf("invalid port number: %s", fs.Arg(1))
		}
	}

	/*
	@obj: ルートディレクトリを絶対パスに変換する
	@ref: IPS3MKEQ-000000-000002 "なお、デフォルトのルートディレクトリはスクリプトを実行した時のカレントディレクトリとする。"
	*/
	absRootDir, err := filepath.Abs(*rootDir)
	if err != nil {
		return fmt.Errorf("failed to get absolute path of root directory: %w", err)
	}

	config := Config{
		Host:    host,
		Port:    port,
		RootDir: absRootDir,
		Opus:    *opus,
	}

	ctx, cancel := context.WithCancel(context.Background())
	w := &Worker{
		config:        config,
		ctx:           ctx,
		cancel:        cancel,
		claudeMsgChan: make(chan ClaudeMessage, 10),
		masterMsgChan: make(chan *common.Message, 10),
	}

	/*
	@obj: シグナルハンドラを設定する
	@ref: IPS3MKEQ-000000-00001C "タスクワーカーのメインループ中に、Ctrl-C（SIG_TERM）が発生したら、切断通知を受け取った時と同じく、起動しているclaude管理コルーチンがあればそれを強制停止し、タスク管理マスタとのコネクションを切断してメインループを終了する。"
	*/
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM)
	go func() {
		<-sigChan
		log.Println("Received interrupt signal, shutting down...")
		w.cancel()
	}()

	return w.run()
}

/*
@obj: ワーカーのメイン処理を実行する
@ref: IPS3MKEQ-000000-000009 "タスクワーカーを起動すると、以下の準備処理を実施する。"
*/
func (w *Worker) run() error {
	/*
	@obj: ルートディレクトリに移動する
	@ref: IPS3MKEQ-000000-00000A "claudeが実行されるディレクトリは、ルートディレクトリとする。"
	*/
	if err := os.Chdir(w.config.RootDir); err != nil {
		return fmt.Errorf("failed to change directory to %s: %w", w.config.RootDir, err)
	}

	/*
	@obj: Claude管理コルーチンを起動し、セッションIDを取得する
	@ref: IPS3MKEQ-000000-00000A "claude管理コルーチンを非同期的に立ち上げ、その中でclaudeをバックグラウンド実行する。"
	@ref: IPS3MKEQ-000000-000005 "この問題を回避するために、タスクワーカーは、起動直後に一度だけclaudeを起動してセッションIDを取得し、保持しておく。"
	@ref: IPS3MKEQ-000000-00003E "この問題を回避するために、タスクワーカーは、起動直後に下記のように一度だけclaudeを起動して、JSON形式の出力の中にあるsession_idの値を取得し、記憶しておく。"
	*/
	sessionID, err := w.createClaudeSession()
	if err != nil {
		return fmt.Errorf("failed to create Claude session: %w", err)
	}
	w.sessionID = sessionID
	log.Printf("Claude session created: %s", sessionID)

	/*
	@obj: Claude管理コルーチンを起動する
	@ref: IPS3MKEQ-000000-00000A "claude管理コルーチンを非同期的に立ち上げ"
	*/
	w.wg.Add(1)
	go w.claudeManagerCoroutine()

	/*
	@obj: タスク管理マスタとの接続を確立する
	@ref: IPS3MKEQ-000000-00000B "タスク管理マスタとのTCPコネクションを確立し、参入メッセージを送る。"
	*/
	conn, err := net.Dial("tcp", fmt.Sprintf("%s:%d", w.config.Host, w.config.Port))
	if err != nil {
		return fmt.Errorf("failed to connect to master: %w", err)
	}
	w.conn = conn
	defer conn.Close()

	/*
	@obj: TCP最適化を設定する
	@ref: IPS3MKEQ-000000-00003D "TCP_NODELAYを設定して即座にメッセージを送信"
	@ref: IPS3MKEQ-000000-00003C "ソケット受信バッファを大きく設定（65536バイト）"
	*/
	if tcpConn, ok := conn.(*net.TCPConn); ok {
		tcpConn.SetNoDelay(true)
		tcpConn.SetReadBuffer(65536)
		tcpConn.SetWriteBuffer(65536)
	}

	/*
	@obj: ワーカーIDを生成する
	@ref: IPS3MKEQ-000000-00000C "ワーカーIDは、タスクワーカーの送信元TCPポート番号とする。"
	*/
	localAddr := conn.LocalAddr().(*net.TCPAddr)
	w.workerID = fmt.Sprintf("worker_%d", localAddr.Port)

	/*
	@obj: 参入メッセージを送信する
	@ref: IPS3MKEQ-000000-00000B "タスク管理マスタとのTCPコネクションを確立し、参入メッセージを送る。"
	@ref: IPS3MKEQ-000000-00000C "参入メッセージには、type=JOINと、msgフィールドにワーカーIDを含める。"
	@ref: IPS3MKEQ-000000-00002F 'JOIN: { "type": "JOIN", "msg": "<ワーカーID>" }'
	*/
	joinMsg := &common.Message{
		Type: common.MessageTypeJoin,
		Msg:  w.workerID,
	}
	if err := w.sendMessage(joinMsg); err != nil {
		return fmt.Errorf("failed to send JOIN message: %w", err)
	}

	/*
	@obj: マスターからのメッセージを受信するゴルーチンを起動する
	*/
	w.wg.Add(1)
	go w.receiveFromMaster()

	/*
	@obj: 参入応答を待つ
	@ref: IPS3MKEQ-000000-00000D "参入メッセージ送信後は、参入応答メッセージ（JOIN_ACK）の受信を待つ。"
	*/
	select {
	case msg := <-w.masterMsgChan:
		if msg.Type != common.MessageTypeJoinAck {
			return fmt.Errorf("expected JOIN_ACK, got %s", msg.Type)
		}
		log.Printf("Worker %s joined successfully", w.workerID)
	case <-time.After(10 * time.Second):
		return fmt.Errorf("timeout waiting for JOIN_ACK")
	case <-w.ctx.Done():
		return nil
	}

	/*
	@obj: メインループを実行する
	@ref: IPS3MKEQ-000000-00000E "参入応答メッセージを受信した後、メインループに入る。メインループでは、継続的に以下のイベントを待つ。"
	*/
	w.mainLoop()

	/*
	@obj: 終了処理を実行する
	@ref: IPS3MKEQ-000000-00001A "ワーカー終了処理では、タスク管理マスタとのコネクションを切断してメインループを終了し、claude管理コルーチンを終了させる。その後、プログラムを終了する。"
	*/
	return w.shutdown()
}

/*
@obj: メインループを実行する
@ref: IPS3MKEQ-000000-00000E "メインループでは、継続的に以下のイベントを待つ。メインループでは長時間のブロッキングが発生しないように工夫する。"
*/
func (w *Worker) mainLoop() {
	/*
	@obj: REQUESTメッセージ受信後の高頻度チェック用
	@ref: IPS3MKEQ-000000-00003B "REQUESTメッセージ受信後5秒間は、メッセージ受信チェックを高頻度（1ミリ秒間隔）で実施"
	*/
	var lastRequestTime time.Time
	checkInterval := 100 * time.Millisecond

	for {
		/*
		@obj: REQUESTメッセージ受信後5秒間は高頻度でチェック
		@ref: IPS3MKEQ-000000-00003B "REQUESTメッセージ受信後5秒間は、メッセージ受信チェックを高頻度（1ミリ秒間隔）で実施"
		*/
		if time.Since(lastRequestTime) < 5*time.Second {
			checkInterval = 1 * time.Millisecond
		} else {
			checkInterval = 100 * time.Millisecond
		}

		select {
		case <-w.ctx.Done():
			return

		case msg := <-w.masterMsgChan:
			/*
			@obj: マスターからのメッセージを処理する
			@ref: IPS3MKEQ-000000-00000F "タスク管理マスタからのヘルスチェック受信"
			@ref: IPS3MKEQ-000000-000010 "タスク管理マスタからのタスク実行依頼受信"
			@ref: IPS3MKEQ-000000-000012 "タスク管理マスタからの切断通知の受信"
			*/
			switch msg.Type {
			case common.MessageTypeCheck:
				w.handleHealthCheck(msg)
			case common.MessageTypeRequest:
				lastRequestTime = time.Now()
				w.handleTaskRequest(msg)
			case common.MessageTypeExit:
				return
			}

		case msg := <-w.claudeMsgChan:
			/*
			@obj: Claude管理コルーチンからの通知を処理する
			@ref: IPS3MKEQ-000000-000011 "claude管理コルーチンからのタスク完了通知（type=DONE/FAILED/USAGE_LIMITED）"
			@ref: IPS3MKEQ-000000-000016 "claude管理コルーチンからのタスク完了通知は、そのままタスク管理マスタにタスク結果報告を送信する。"
			*/
			w.handleClaudeResult(msg)

		case <-time.After(checkInterval):
			// 定期的なチェック
		}
	}
}

/*
@obj: Claudeセッションを作成する
@ref: IPS3MKEQ-000000-00003E "タスクワーカーは、起動直後に下記のように一度だけclaudeを起動して、JSON形式の出力の中にあるsession_idの値を取得し、記憶しておく。これがセッションIDなので、後のタスク実行時に利用する。"
@ref: IPS3MKEQ-000000-000045 'このツールのオプショナルの引数に--opusが指定されていた場合: `claude --allowedTools WebFetch,Read,Write,Bash --model opus -p "." --output-format json`'
@ref: IPS3MKEQ-000000-000046 'このツールのオプショナルの引数指定がない場合: `claude -r <セッションID> --allowedTools WebFetch,Read,Write,Bash -p "." --output-format json`'
@ref: IPS3MKEQ-000000-000041 "取得したセッションIDをデバッグメッセージとして出力すること。"
*/
func (w *Worker) createClaudeSession() (string, error) {
	var cmdArgs []string
	if w.config.Opus {
		cmdArgs = []string{"claude", "--allowedTools", "WebFetch,Read,Write,Bash", "--model", "opus", "--output-format", "json", "-p", "."}
	} else {
		cmdArgs = []string{"claude", "--allowedTools", "WebFetch,Read,Write,Bash", "--model", "sonnet", "--output-format", "json", "-p", "."}
	}

	cmd := exec.CommandContext(w.ctx, cmdArgs[0], cmdArgs[1:]...)
	cmd.Dir = w.config.RootDir
	output, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("failed to execute claude: %w (output: %s)", err, string(output))
	}

	/*
	@obj: JSON出力からセッションIDを抽出する
	@ref: IPS3MKEQ-000006-000001 "Claude CLIでは`--output-format json`オプションを使用することで、レスポンスにセッションIDが含まれます。"
	*/
	var response struct {
		SessionID string `json:"session_id"`
		IsError   bool   `json:"is_error"`
	}
	if err := json.Unmarshal(output, &response); err != nil {
		return "", fmt.Errorf("failed to parse claude response: %w", err)
	}

	if response.IsError {
		return "", fmt.Errorf("claude returned error")
	}

	/*
	@obj: セッションIDをデバッグ出力する
	@ref: IPS3MKEQ-000000-000041 "取得したセッションIDをデバッグメッセージとして出力すること。"
	*/
	log.Printf("Debug: Claude session ID: %s", response.SessionID)

	return response.SessionID, nil
}
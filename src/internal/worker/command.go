package worker

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"

	"github.com/sirupsen/logrus"
)

// Run はworkerコマンドのエントリーポイント
// @obj: workerコマンドの引数解析と実行
// @ref: SCIK9X27-000001-000000, SCIK9X27-000001-000001, SCIK9X27-000001-000002, SCIK9X27-000001-000003
func Run(args []string) error {
	// フラグセットの作成
	fs := flag.NewFlagSet("worker", flag.ContinueOnError)

	// @obj: コマンドライン引数の定義
	// @ref: SCIK9X27-000001-000001 - ホスト名とポート番号（デフォルト: localhost:34567）
	host := fs.String("host", "localhost", "Master server hostname or IP address")
	port := fs.Int("port", 34567, "Master server port number")
	
	// @obj: ルートディレクトリパスの引数
	// @ref: SCIK9X27-000001-000002 - ルートディレクトリパス（オプショナル）
	rootDir := fs.String("root", ".", "Root directory path (default: current directory)")
	
	// @obj: --opusオプションの定義
	// @ref: SCIK9X27-000001-000003 - claudeのモデル指定
	opus := fs.Bool("opus", false, "Use Claude Opus model instead of Sonnet")
	
	// ヘルプメッセージのカスタマイズ
	fs.Usage = func() {
		fmt.Fprintf(os.Stderr, "Usage: taskorganizer worker [options]\n")
		fmt.Fprintf(os.Stderr, "\nOptions:\n")
		fs.PrintDefaults()
		fmt.Fprintf(os.Stderr, "\nDescription:\n")
		fmt.Fprintf(os.Stderr, "  Starts a task worker that connects to the task management master.\n")
		fmt.Fprintf(os.Stderr, "  The worker receives tasks from the master and executes them using Claude AI.\n")
		fmt.Fprintf(os.Stderr, "\nExamples:\n")
		fmt.Fprintf(os.Stderr, "  taskorganizer worker                    # Connect to localhost:34567\n")
		fmt.Fprintf(os.Stderr, "  taskorganizer worker -host 192.168.1.10 # Connect to specific host\n")
		fmt.Fprintf(os.Stderr, "  taskorganizer worker --opus             # Use Claude Opus model\n")
	}

	// 引数のパース
	if err := fs.Parse(args); err != nil {
		return err
	}

	// ロガーの設定
	logger := logrus.New()
	logger.SetLevel(logrus.InfoLevel)
	
	// 環境変数でデバッグレベルを設定可能
	if os.Getenv("DEBUG") != "" {
		logger.SetLevel(logrus.DebugLevel)
	}

	// フォーマッターの設定
	logger.SetFormatter(&logrus.TextFormatter{
		FullTimestamp:   true,
		TimestampFormat: "2006-01-02 15:04:05",
	})

	// 絶対パスに変換
	absRootDir, err := filepath.Abs(*rootDir)
	if err != nil {
		return fmt.Errorf("failed to get absolute path of root directory: %w", err)
	}

	// config.ymlは使用しない（仕様で削除済み）

	logger.Infof("Root directory: %s", absRootDir)
	logger.Infof("Master server: %s:%d", *host, *port)
	if *opus {
		logger.Info("Using Claude Opus model")
	} else {
		logger.Info("Using Claude Sonnet model")
	}

	// TaskWorkerインスタンスの作成と実行
	worker := NewTaskWorker(*host, *port, absRootDir, *opus, logger)
	return worker.Run()
}
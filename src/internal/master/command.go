package master

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"

	"github.com/sirupsen/logrus"
)

// Run はmasterコマンドのエントリーポイント
// @obj: masterコマンドの引数解析と実行
// @ref: SCIK9X27-000003-000000, SCIK9X27-000003-000001, SCIK9X27-000003-000002
func Run(args []string) error {
	// フラグセットの作成
	fs := flag.NewFlagSet("master", flag.ContinueOnError)

	// @obj: コマンドライン引数の定義
	// @ref: SCIK9X27-000003-000001 - ポート番号（デフォルト: 34567）
	port := fs.Int("port", 34567, "Port number to listen on")
	
	// @obj: ルートディレクトリパスの引数
	// @ref: SCIK9X27-000003-000002 - ルートディレクトリパス（オプショナル）
	rootDir := fs.String("root", ".", "Root directory path (default: current directory)")
	
	// ヘルプメッセージのカスタマイズ
	fs.Usage = func() {
		fmt.Fprintf(os.Stderr, "Usage: taskorganizer master [options]\n")
		fmt.Fprintf(os.Stderr, "\nOptions:\n")
		fs.PrintDefaults()
		fmt.Fprintf(os.Stderr, "\nDescription:\n")
		fmt.Fprintf(os.Stderr, "  Starts the task management master server that distributes tasks to workers.\n")
		fmt.Fprintf(os.Stderr, "  The master reads tasks from .tasks/pending/ and assigns them to connected workers.\n")
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

	// @obj: .tasks/ディレクトリの存在確認
	// @ref: SCIK9X27-000003-000006 - .tasks/ディレクトリが必要
	tasksDir := filepath.Join(absRootDir, ".tasks")
	if _, err := os.Stat(tasksDir); os.IsNotExist(err) {
		return fmt.Errorf(".tasks directory not found at %s. Please run 'taskorganizer create' first", tasksDir)
	}

	// 必要なサブディレクトリの確認
	requiredDirs := []string{"pending", "working", "done", "failed"}
	for _, dir := range requiredDirs {
		dirPath := filepath.Join(tasksDir, dir)
		if _, err := os.Stat(dirPath); os.IsNotExist(err) {
			return fmt.Errorf("required directory %s not found", dirPath)
		}
	}

	logger.Infof("Root directory: %s", absRootDir)
	logger.Infof("Port: %d", *port)

	// TaskMasterインスタンスの作成と実行
	master := NewTaskMaster(absRootDir, *port, logger)
	return master.Run()
}
package create

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"

	"github.com/sirupsen/logrus"
)

// Run はcreateコマンドのエントリーポイント
// @obj: createコマンドの引数解析と実行
// @ref: SCIK9X27-000002-000000, SCIK9X27-000002-000001, SCIK9X27-000002-000002, SCIK9X27-000002-000003
func Run(args []string) error {
	// フラグセットの作成
	fs := flag.NewFlagSet("create", flag.ContinueOnError)

	// @obj: コマンドライン引数の定義
	// @ref: SCIK9X27-000002-000002 - ルートディレクトリパスのオプショナル引数
	rootDir := fs.String("root", ".", "Root directory path (default: current directory)")
	
	// @obj: --clearオプションの定義
	// @ref: SCIK9X27-000002-000003 - 既存の.tasks/ディレクトリを削除するオプション
	clearTasks := fs.Bool("clear", false, "Clear existing .tasks directory before creating new tasks")
	
	// ヘルプメッセージのカスタマイズ
	fs.Usage = func() {
		fmt.Fprintf(os.Stderr, "Usage: taskorganizer create [options] <business_prompt_file> <target_list_file>\n")
		fmt.Fprintf(os.Stderr, "\nOptions:\n")
		fs.PrintDefaults()
		fmt.Fprintf(os.Stderr, "\nArguments:\n")
		fmt.Fprintf(os.Stderr, "  business_prompt_file  Path to the business prompt text file\n")
		fmt.Fprintf(os.Stderr, "  target_list_file      Path to the target list file (one target per line)\n")
	}

	// 引数のパース
	if err := fs.Parse(args); err != nil {
		return err
	}

	// @obj: 必須引数の確認
	// @ref: SCIK9X27-000002-000001 - 業務プロンプトテキストファイルとターゲットリストファイルが必須
	if fs.NArg() != 2 {
		fs.Usage()
		return fmt.Errorf("exactly 2 arguments required")
	}

	businessPromptPath := fs.Arg(0)
	targetListPath := fs.Arg(1)

	// ロガーの設定
	logger := logrus.New()
	logger.SetLevel(logrus.InfoLevel)
	
	// 環境変数でデバッグレベルを設定可能
	if os.Getenv("DEBUG") != "" {
		logger.SetLevel(logrus.DebugLevel)
	}

	// 絶対パスに変換
	absRootDir, err := filepath.Abs(*rootDir)
	if err != nil {
		return fmt.Errorf("failed to get absolute path of root directory: %w", err)
	}

	// @obj: 引数のファイルパスを絶対パスに変換
	// @ref: SCIK9X27-000002-000001 - ファイルパスの処理
	absBusinessPromptPath, err := filepath.Abs(businessPromptPath)
	if err != nil {
		return fmt.Errorf("failed to get absolute path of business prompt file: %w", err)
	}

	absTargetListPath, err := filepath.Abs(targetListPath)
	if err != nil {
		return fmt.Errorf("failed to get absolute path of target list file: %w", err)
	}

	// ファイルの存在確認
	if _, err := os.Stat(absBusinessPromptPath); os.IsNotExist(err) {
		return fmt.Errorf("business prompt file not found: %s", absBusinessPromptPath)
	}

	if _, err := os.Stat(absTargetListPath); os.IsNotExist(err) {
		return fmt.Errorf("target list file not found: %s", absTargetListPath)
	}

	logger.Infof("Root directory: %s", absRootDir)
	logger.Infof("Business prompt file: %s", absBusinessPromptPath)
	logger.Infof("Target list file: %s", absTargetListPath)
	if *clearTasks {
		logger.Info("Clear tasks option: enabled")
	}

	// TaskCreatorインスタンスの作成と実行
	creator := NewTaskCreator(absBusinessPromptPath, absTargetListPath, absRootDir, *clearTasks, logger)
	return creator.Run()
}
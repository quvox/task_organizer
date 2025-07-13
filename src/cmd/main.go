package main

import (
	"fmt"
	"os"

	"github.com/quvox/task_organizer/internal/create"
	"github.com/quvox/task_organizer/internal/master"
	"github.com/quvox/task_organizer/internal/worker"
)

// main はTask Organizerのエントリーポイント
// @obj: コマンドライン引数に基づいて適切なサブコマンドを実行する
// @ref: SCIK9X27-000008-000000, SCIK9X27-000008-000001 - Task Organizerツールの主要目的
func main() {
	if len(os.Args) < 2 {
		printUsage()
		os.Exit(1)
	}

	// サブコマンドの処理
	// @obj: 3つの主要機能（タスク生成、タスク管理、タスクワーカー）を振り分ける
	// @ref: SCIK9X27-000008-000004 - 3つの主要機能の実装
	switch os.Args[1] {
	case "create":
		// タスク生成ツールの実行
		// @obj: ビジネスプロンプトとターゲットリストからタスクを生成
		// @ref: SCIK9X27-000002-000000 - タスク生成ツールの起動コマンド
		if err := create.Run(os.Args[2:]); err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}

	case "master":
		// タスク管理マスターの実行
		// @obj: タスクワーカーとの接続を管理し、タスクを分配
		// @ref: SCIK9X27-000003-000000 - タスク管理マスターの起動コマンド
		if err := master.Run(os.Args[2:]); err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}

	case "worker":
		// タスクワーカーの実行
		// @obj: タスク管理マスターから指示を受けてAIエージェントでタスクを実行
		// @ref: SCIK9X27-000001-000000 - タスクワーカーの起動コマンド
		if err := worker.Run(os.Args[2:]); err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}

	default:
		fmt.Fprintf(os.Stderr, "Unknown command: %s\n", os.Args[1])
		printUsage()
		os.Exit(1)
	}
}

// printUsage はツールの使用方法を表示
// @obj: ユーザーに使用可能なコマンドを案内
// @ref: SCIK9X27-000008-000004 - 3つの主要機能の説明
func printUsage() {
	fmt.Println("Usage: taskorganizer <command> [arguments]")
	fmt.Println()
	fmt.Println("Commands:")
	fmt.Println("  create  - Create tasks from business prompt and target list")
	fmt.Println("  master  - Start task management master server")
	fmt.Println("  worker  - Start task worker client")
	fmt.Println()
	fmt.Println("Use 'taskorganizer <command> -h' for more information about a command.")
}
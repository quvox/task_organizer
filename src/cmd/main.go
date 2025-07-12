package main

import (
	"fmt"
	"os"

	"github.com/quvox/task_organizer/internal/create"
	"github.com/quvox/task_organizer/internal/master"
	"github.com/quvox/task_organizer/internal/worker"
)

/*
@obj: タスク管理システムのメインエントリーポイント
@ref: IPS3MKEQ-000001-000000, IPS3MKEQ-000002-000000, IPS3MKEQ-000000-000000
*/
func main() {
	if len(os.Args) < 2 {
		printUsage()
		os.Exit(1)
	}

	command := os.Args[1]

	switch command {
	case "create":
		/*
		@obj: タスク生成ツールを起動する
		@ref: IPS3MKEQ-000001-000000 "タスク生成ツールは、`taskorganizer create`で起動する。"
		*/
		if err := create.Run(os.Args[2:]); err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}
	case "master":
		/*
		@obj: タスク管理マスタを起動する
		@ref: IPS3MKEQ-000002-000000 "タスク管理マスタは、`taskorganizer master`で起動する。"
		*/
		if err := master.Run(os.Args[2:]); err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}
	case "worker":
		/*
		@obj: タスクワーカーを起動する
		@ref: IPS3MKEQ-000000-000000 "タスクワーカーは、`taskorganizer worker`で起動する。"
		*/
		if err := worker.Run(os.Args[2:]); err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}
	default:
		printUsage()
		os.Exit(1)
	}
}

func printUsage() {
	fmt.Println("Usage:")
	fmt.Println("  taskorganizer create <business_prompt_file> <target_list_file> [options]")
	fmt.Println("  taskorganizer master [port] [options]")
	fmt.Println("  taskorganizer worker [hostname] [port] [options]")
	fmt.Println()
	fmt.Println("Options:")
	fmt.Println("  --root-dir <path>  Root directory path")
	fmt.Println("  --clear           Clear existing .tasks directory (create only)")
	fmt.Println("  --opus            Use Opus model (worker only)")
}
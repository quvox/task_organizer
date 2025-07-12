package create

import (
	"bufio"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

/*
@obj: タスク生成ツールのメイン処理
@ref: IPS3MKEQ-000001-000000 "タスク生成ツールは、`taskorganizer create`で起動する。"
*/
func Run(args []string) error {
	/*
	@obj: コマンドライン引数を解析する
	@ref: IPS3MKEQ-000001-000001 "タスク生成ツールには、2つのテキストファイルパスを引数で与える。一つは業務プロンプトテキスト、もう一つはターゲットリストである。"
	@ref: IPS3MKEQ-000001-000002 "タスク生成ツールには、さらにオプショナル引数でルートディレクトリパスを指定する。"
	@ref: IPS3MKEQ-000001-000003 'オプショナルの引数"--clear"を指定した場合、ルートディレクトリにすでに存在している.tasks/ディレクトリを起動時に全て削除する。'
	*/
	fs := flag.NewFlagSet("create", flag.ExitOnError)
	rootDir := fs.String("root-dir", ".", "Root directory path")
	clear := fs.Bool("clear", false, "Clear existing .tasks directory on startup")

	if err := fs.Parse(args); err != nil {
		return err
	}

	if fs.NArg() < 2 {
		return fmt.Errorf("usage: taskorganizer create <business_prompt_file> <target_list_file> [options]")
	}

	businessPromptFile := fs.Arg(0)
	targetListFile := fs.Arg(1)

	/*
	@obj: ルートディレクトリを絶対パスに変換する
	@ref: IPS3MKEQ-000001-000002 "なお、デフォルトのルートディレクトリはスクリプトを実行した時のカレントディレクトリとする。"
	*/
	absRootDir, err := filepath.Abs(*rootDir)
	if err != nil {
		return fmt.Errorf("failed to get absolute path of root directory: %w", err)
	}

	/*
	@obj: --clearオプションが指定された場合、既存の.tasksディレクトリを削除する
	@ref: IPS3MKEQ-000001-000003 'オプショナルの引数"--clear"を指定した場合、ルートディレクトリにすでに存在している.tasks/ディレクトリを起動時に全て削除する。'
	*/
	tasksDir := filepath.Join(absRootDir, ".tasks")
	if *clear && exists(tasksDir) {
		if err := os.RemoveAll(tasksDir); err != nil {
			return fmt.Errorf("failed to remove existing .tasks directory: %w", err)
		}
	}

	/*
	@obj: .tasksディレクトリとサブディレクトリを作成する
	@ref: IPS3MKEQ-000001-000006 "タスク生成ツールを起動すると、ルートディレクトリの下に、.tasks/ディレクトリを生成し、さらにその下に、pending、working、done、failedというサブディレクトリを作る。"
	*/
	subdirs := []string{"pending", "working", "done", "failed"}
	for _, subdir := range subdirs {
		dir := filepath.Join(tasksDir, subdir)
		if err := os.MkdirAll(dir, 0755); err != nil {
			return fmt.Errorf("failed to create directory %s: %w", dir, err)
		}
	}

	/*
	@obj: 業務プロンプトを読み込む
	@ref: IPS3MKEQ-000001-000004 "業務プロンプトには、実行させたい業務がテキストで記述されている。"
	*/
	businessPrompt, err := readFile(businessPromptFile)
	if err != nil {
		return fmt.Errorf("failed to read business prompt file: %w", err)
	}

	/*
	@obj: ターゲットリストを読み込む
	@ref: IPS3MKEQ-000001-000005 "ターゲットリストには、業務対象のリストが改行区切りのテキストで記述されている。"
	*/
	targets, err := readLines(targetListFile)
	if err != nil {
		return fmt.Errorf("failed to read target list file: %w", err)
	}

	/*
	@obj: 各ターゲットに対してタスクプロンプトを生成する
	@ref: IPS3MKEQ-000001-000007 "タスク生成ツールは、ターゲットリスト1行1行に対して、タスクプロンプトを生成し、.tasks/pending/の下にテキストファイルとして出力する。"
	*/
	pendingDir := filepath.Join(tasksDir, "pending")
	for i, target := range targets {
		/*
		@obj: タスクプロンプトのテキストを生成する
		@ref: IPS3MKEQ-000001-000008 "タスクプロンプトには、以下のテキストを書き出す。＜業務プロンプトの内容＞の部分には、ツールに業務プロンプトテキストを、＜ターゲットリストのエントリ＞の部分には、リストから読み取った1行を書く。"
		@ref: IPS3MKEQ-000001-000009 "あなたは、有能なビジネスマンです。上司から、「＜業務プロンプトの内容＞」というミッションを与えられました。..."
		*/
		taskPrompt := fmt.Sprintf(
			"あなたは、有能なビジネスマンです。上司から、「%s」というミッションを与えられました。一緒に与えられたリストを複数のビジネスマンが分担して取り組みます。あなたが割り当てられたのは、%sです。上司からの指示に従ってミッションを実行してください。",
			businessPrompt,
			target,
		)

		/*
		@obj: タスクプロンプトをファイルに書き出す
		@ref: IPS3MKEQ-000001-000007 ".tasks/pending/の下にテキストファイルとして出力する。"
		*/
		filename := fmt.Sprintf("task_%05d.txt", i+1)
		filepath := filepath.Join(pendingDir, filename)
		if err := writeFile(filepath, taskPrompt); err != nil {
			return fmt.Errorf("failed to write task file %s: %w", filename, err)
		}
	}

	fmt.Printf("Successfully created %d task files in %s\n", len(targets), pendingDir)
	return nil
}

/*
@obj: ファイルの存在を確認する
@ref: IPS3MKEQ-000001-000003 "ルートディレクトリにすでに存在している.tasks/ディレクトリ"
*/
func exists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

/*
@obj: ファイルの内容を読み込む
@ref: IPS3MKEQ-000001-000004 "業務プロンプトには、実行させたい業務がテキストで記述されている。"
*/
func readFile(filename string) (string, error) {
	content, err := os.ReadFile(filename)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(content)), nil
}

/*
@obj: ファイルから行単位でデータを読み込む
@ref: IPS3MKEQ-000001-000005 "ターゲットリストには、業務対象のリストが改行区切りのテキストで記述されている。"
*/
func readLines(filename string) ([]string, error) {
	file, err := os.Open(filename)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	var lines []string
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line != "" {
			lines = append(lines, line)
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}

	return lines, nil
}

/*
@obj: ファイルに内容を書き込む
@ref: IPS3MKEQ-000001-000007 ".tasks/pending/の下にテキストファイルとして出力する。"
*/
func writeFile(filename, content string) error {
	return os.WriteFile(filename, []byte(content), 0644)
}
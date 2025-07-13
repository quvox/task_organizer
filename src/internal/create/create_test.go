package create

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sirupsen/logrus"
)

// TestTaskCreator_createTaskDirectories はディレクトリ作成をテストする
// @obj: .tasks/ディレクトリ構造の作成確認
// @ref: SCIK9X27-000002-000006 - pending, working, done, failedディレクトリの作成
func TestTaskCreator_createTaskDirectories(t *testing.T) {
	// テスト用の一時ディレクトリを作成
	tempDir := t.TempDir()
	
	logger := logrus.New()
	logger.SetLevel(logrus.DebugLevel)
	
	tc := &TaskCreator{
		rootDir: tempDir,
		logger:  logger,
	}
	
	// ディレクトリ作成を実行
	err := tc.createTaskDirectories()
	if err != nil {
		t.Fatalf("createTaskDirectories failed: %v", err)
	}
	
	// 期待されるディレクトリが存在することを確認
	expectedDirs := []string{
		".tasks",
		".tasks/pending",
		".tasks/working",
		".tasks/done",
		".tasks/failed",
	}
	
	for _, dir := range expectedDirs {
		path := filepath.Join(tempDir, dir)
		info, err := os.Stat(path)
		if err != nil {
			t.Errorf("Directory %s does not exist: %v", dir, err)
			continue
		}
		if !info.IsDir() {
			t.Errorf("%s is not a directory", dir)
		}
	}
}

// TestTaskCreator_readBusinessPrompt は業務プロンプトの読み込みをテストする
// @obj: 業務プロンプトファイルの読み込み確認
// @ref: SCIK9X27-000002-000004 - 業務プロンプトテキストファイルの読み込み
func TestTaskCreator_readBusinessPrompt(t *testing.T) {
	tempDir := t.TempDir()
	
	// テスト用の業務プロンプトファイルを作成
	promptContent := "これはテスト用の業務プロンプトです。\n複数行にわたる\n内容を含みます。"
	promptFile := filepath.Join(tempDir, "business_prompt.txt")
	err := os.WriteFile(promptFile, []byte(promptContent), 0644)
	if err != nil {
		t.Fatalf("Failed to create test prompt file: %v", err)
	}
	
	logger := logrus.New()
	tc := &TaskCreator{
		businessPromptPath: promptFile,
		logger:             logger,
	}
	
	// 業務プロンプトを読み込む
	prompt, err := tc.readBusinessPrompt()
	if err != nil {
		t.Fatalf("readBusinessPrompt failed: %v", err)
	}
	
	// 前後の空白が削除されていることを確認
	expectedPrompt := strings.TrimSpace(promptContent)
	if prompt != expectedPrompt {
		t.Errorf("Prompt mismatch:\ngot:  %q\nwant: %q", prompt, expectedPrompt)
	}
}

// TestTaskCreator_createTasks はタスク生成をテストする
// @obj: ターゲットリストからのタスクプロンプト生成確認
// @ref: SCIK9X27-000002-000007, SCIK9X27-000002-000008, SCIK9X27-000002-000009
func TestTaskCreator_createTasks(t *testing.T) {
	tempDir := t.TempDir()
	
	// テスト用のターゲットリストファイルを作成
	targets := []string{
		"target1",
		"target2",
		"", // 空行（スキップされるべき）
		"target3",
		"  target4  ", // 前後に空白（トリムされるべき）
	}
	targetFile := filepath.Join(tempDir, "targets.txt")
	err := os.WriteFile(targetFile, []byte(strings.Join(targets, "\n")), 0644)
	if err != nil {
		t.Fatalf("Failed to create target file: %v", err)
	}
	
	// .tasks/pendingディレクトリを作成
	pendingDir := filepath.Join(tempDir, ".tasks", "pending")
	err = os.MkdirAll(pendingDir, 0755)
	if err != nil {
		t.Fatalf("Failed to create pending directory: %v", err)
	}
	
	logger := logrus.New()
	tc := &TaskCreator{
		targetListPath: targetFile,
		rootDir:        tempDir,
		logger:         logger,
	}
	
	businessPrompt := "テスト業務プロンプト"
	
	// タスクを生成
	err = tc.createTasks(businessPrompt)
	if err != nil {
		t.Fatalf("createTasks failed: %v", err)
	}
	
	// 生成されたタスクファイルを確認
	files, err := os.ReadDir(pendingDir)
	if err != nil {
		t.Fatalf("Failed to read pending directory: %v", err)
	}
	
	// 空行を除いた4つのタスクが生成されるべき
	if len(files) != 4 {
		t.Errorf("Expected 4 task files, got %d", len(files))
	}
	
	// 各タスクファイルの内容を確認
	expectedTargets := []string{"target1", "target2", "target3", "target4"}
	for i, file := range files {
		expectedName := fmt.Sprintf("task_%06d.txt", i)
		if file.Name() != expectedName {
			t.Errorf("Expected filename %s, got %s", expectedName, file.Name())
		}
		
		// ファイル内容を確認
		content, err := os.ReadFile(filepath.Join(pendingDir, file.Name()))
		if err != nil {
			t.Errorf("Failed to read task file: %v", err)
			continue
		}
		
		// プロンプトに期待されるターゲットが含まれているか確認
		if i < len(expectedTargets) {
			if !strings.Contains(string(content), expectedTargets[i]) {
				t.Errorf("Task file %s does not contain expected target %s", file.Name(), expectedTargets[i])
			}
		}
		
		// プロンプトのフォーマットを確認
		if !strings.Contains(string(content), businessPrompt) {
			t.Errorf("Task file does not contain business prompt")
		}
		if !strings.Contains(string(content), "あなたは、有能なビジネスマンです") {
			t.Errorf("Task file does not contain expected template text")
		}
	}
}

// TestTaskCreator_Run は全体的な実行をテストする
// @obj: タスク生成ツールの統合的な動作確認
// @ref: SCIK9X27-000002-000003, SCIK9X27-000002-000006, SCIK9X27-000002-000007
func TestTaskCreator_Run(t *testing.T) {
	tempDir := t.TempDir()
	
	// テスト用のファイルを準備
	businessPrompt := "URLにアクセスして要約を作成してください"
	promptFile := filepath.Join(tempDir, "prompt.txt")
	err := os.WriteFile(promptFile, []byte(businessPrompt), 0644)
	if err != nil {
		t.Fatalf("Failed to create prompt file: %v", err)
	}
	
	targets := []string{
		"https://example.com",
		"https://example.org",
		"https://example.net",
	}
	targetFile := filepath.Join(tempDir, "urls.txt")
	err = os.WriteFile(targetFile, []byte(strings.Join(targets, "\n")), 0644)
	if err != nil {
		t.Fatalf("Failed to create target file: %v", err)
	}
	
	logger := logrus.New()
	
	// テスト1: 通常実行
	t.Run("Normal execution", func(t *testing.T) {
		tc := NewTaskCreator(promptFile, targetFile, tempDir, false, logger)
		err := tc.Run()
		if err != nil {
			t.Fatalf("Run failed: %v", err)
		}
		
		// タスクファイルが生成されていることを確認
		pendingDir := filepath.Join(tempDir, ".tasks", "pending")
		files, err := os.ReadDir(pendingDir)
		if err != nil {
			t.Fatalf("Failed to read pending directory: %v", err)
		}
		
		if len(files) != len(targets) {
			t.Errorf("Expected %d task files, got %d", len(targets), len(files))
		}
	})
	
	// テスト2: --clearオプション付き実行
	t.Run("With clear option", func(t *testing.T) {
		// 既存のタスクファイルを作成
		dummyFile := filepath.Join(tempDir, ".tasks", "pending", "dummy.txt")
		os.WriteFile(dummyFile, []byte("dummy"), 0644)
		
		tc := NewTaskCreator(promptFile, targetFile, tempDir, true, logger)
		err := tc.Run()
		if err != nil {
			t.Fatalf("Run with clear option failed: %v", err)
		}
		
		// dummyファイルが削除されていることを確認
		if _, err := os.Stat(dummyFile); !os.IsNotExist(err) {
			t.Error("Clear option did not remove existing files")
		}
	})
}

// TestTaskCreator_ErrorHandling はエラーハンドリングをテストする
// @obj: 各種エラー条件での適切な処理確認
// @ref: SCIK9X27-000002-000004, SCIK9X27-000002-000005
func TestTaskCreator_ErrorHandling(t *testing.T) {
	tempDir := t.TempDir()
	logger := logrus.New()
	
	t.Run("Non-existent business prompt file", func(t *testing.T) {
		tc := &TaskCreator{
			businessPromptPath: filepath.Join(tempDir, "non_existent.txt"),
			logger:             logger,
		}
		
		_, err := tc.readBusinessPrompt()
		if err == nil {
			t.Error("Expected error for non-existent prompt file")
		}
	})
	
	t.Run("Non-existent target list file", func(t *testing.T) {
		tc := &TaskCreator{
			targetListPath: filepath.Join(tempDir, "non_existent_targets.txt"),
			rootDir:        tempDir,
			logger:         logger,
		}
		
		err := tc.createTasks("test prompt")
		if err == nil {
			t.Error("Expected error for non-existent target file")
		}
	})
	
	t.Run("Invalid directory permissions", func(t *testing.T) {
		// 読み取り専用ディレクトリを作成
		readOnlyDir := filepath.Join(tempDir, "readonly")
		os.Mkdir(readOnlyDir, 0555)
		defer os.Chmod(readOnlyDir, 0755) // クリーンアップ
		
		tc := &TaskCreator{
			rootDir: readOnlyDir,
			logger:  logger,
		}
		
		err := tc.createTaskDirectories()
		if err == nil {
			t.Error("Expected error for read-only directory")
		}
	})
}
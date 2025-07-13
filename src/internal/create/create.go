package create

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/sirupsen/logrus"
)

// TaskCreator はタスク生成機能を提供する構造体
// @obj タスク生成ツールの主要な構造体
// @ref SCIK9X27-000002-000000
type TaskCreator struct {
	businessPromptPath string
	targetListPath     string
	rootDir            string
	clearTasks         bool
	logger             *logrus.Logger
}

// NewTaskCreator は新しいTaskCreatorインスタンスを作成する
// @obj タスク生成ツールのインスタンス生成
// @ref SCIK9X27-000002-000001, SCIK9X27-000002-000002, SCIK9X27-000002-000003
func NewTaskCreator(businessPromptPath, targetListPath, rootDir string, clearTasks bool, logger *logrus.Logger) *TaskCreator {
	return &TaskCreator{
		businessPromptPath: businessPromptPath,
		targetListPath:     targetListPath,
		rootDir:            rootDir,
		clearTasks:         clearTasks,
		logger:             logger,
	}
}

// Run はタスク生成処理を実行する
// @obj タスク生成の主処理
// @ref SCIK9X27-000002-000003, SCIK9X27-000002-000006, SCIK9X27-000002-000007
func (tc *TaskCreator) Run() error {
	tc.logger.Info("Starting task creation")

	// @obj .tasks/ディレクトリのクリア処理
	// @ref SCIK9X27-000002-000003
	if tc.clearTasks {
		tasksDir := filepath.Join(tc.rootDir, ".tasks")
		if _, err := os.Stat(tasksDir); err == nil {
			tc.logger.Info("Clearing existing .tasks directory")
			if err := os.RemoveAll(tasksDir); err != nil {
				return fmt.Errorf("failed to remove .tasks directory: %w", err)
			}
		}
	}

	// @obj .tasks/ディレクトリ構造の作成
	// @ref SCIK9X27-000002-000006
	if err := tc.createTaskDirectories(); err != nil {
		return err
	}

	// @obj 業務プロンプトの読み込み
	// @ref SCIK9X27-000002-000004
	businessPrompt, err := tc.readBusinessPrompt()
	if err != nil {
		return err
	}

	// @obj ターゲットリストを読み込んでタスクプロンプトを生成
	// @ref SCIK9X27-000002-000005, SCIK9X27-000002-000007
	if err := tc.createTasks(businessPrompt); err != nil {
		return err
	}

	tc.logger.Info("Task creation completed")
	return nil
}

// createTaskDirectories はタスク管理用ディレクトリを作成する
// @obj .tasks/以下のディレクトリ構造を作成
// @ref SCIK9X27-000002-000006
func (tc *TaskCreator) createTaskDirectories() error {
	dirs := []string{
		filepath.Join(tc.rootDir, ".tasks"),
		filepath.Join(tc.rootDir, ".tasks", "pending"),
		filepath.Join(tc.rootDir, ".tasks", "working"),
		filepath.Join(tc.rootDir, ".tasks", "done"),
		filepath.Join(tc.rootDir, ".tasks", "failed"),
	}

	for _, dir := range dirs {
		if err := os.MkdirAll(dir, 0755); err != nil {
			return fmt.Errorf("failed to create directory %s: %w", dir, err)
		}
		tc.logger.Debugf("Created directory: %s", dir)
	}

	return nil
}

// readBusinessPrompt は業務プロンプトファイルを読み込む
// @obj 業務プロンプトテキストの読み込み
// @ref SCIK9X27-000002-000004
func (tc *TaskCreator) readBusinessPrompt() (string, error) {
	file, err := os.Open(tc.businessPromptPath)
	if err != nil {
		return "", fmt.Errorf("failed to open business prompt file: %w", err)
	}
	defer file.Close()

	content, err := io.ReadAll(file)
	if err != nil {
		return "", fmt.Errorf("failed to read business prompt file: %w", err)
	}

	prompt := strings.TrimSpace(string(content))
	tc.logger.Debugf("Business prompt loaded: %d characters", len(prompt))
	return prompt, nil
}

// createTasks はターゲットリストからタスクプロンプトファイルを生成する
// @obj ターゲットリストの各行に対してタスクプロンプトを生成
// @ref SCIK9X27-000002-000007, SCIK9X27-000002-000008, SCIK9X27-000002-000009
func (tc *TaskCreator) createTasks(businessPrompt string) error {
	file, err := os.Open(tc.targetListPath)
	if err != nil {
		return fmt.Errorf("failed to open target list file: %w", err)
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	taskCount := 0

	for scanner.Scan() {
		target := strings.TrimSpace(scanner.Text())
		if target == "" {
			continue
		}

		// @obj タスクプロンプトの生成
		// @ref SCIK9X27-000002-000008, SCIK9X27-000002-000009
		taskPrompt := fmt.Sprintf(
			"あなたは、有能なビジネスマンです。上司から、「%s」というミッションを与えられました。"+
				"一緒に与えられたリストを複数のビジネスマンが分担して取り組みます。"+
				"あなたが割り当てられたのは、%sです。上司からの指示に従ってミッションを実行してください。",
			businessPrompt,
			target,
		)

		// @obj タスクプロンプトファイルの書き出し
		// @ref SCIK9X27-000002-000007
		taskFileName := fmt.Sprintf("task_%06d.txt", taskCount)
		taskFilePath := filepath.Join(tc.rootDir, ".tasks", "pending", taskFileName)

		if err := os.WriteFile(taskFilePath, []byte(taskPrompt), 0644); err != nil {
			return fmt.Errorf("failed to write task file %s: %w", taskFileName, err)
		}

		tc.logger.Infof("Created task file: %s (target: %s)", taskFileName, target)
		taskCount++
	}

	if err := scanner.Err(); err != nil {
		return fmt.Errorf("error reading target list: %w", err)
	}

	tc.logger.Infof("Total tasks created: %d", taskCount)
	return nil
}
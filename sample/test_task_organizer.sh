#!/bin/bash

# Task Organizerのテストスクリプト

echo "Task Organizerテストを開始します..."

# resultsディレクトリを作成
echo "1. resultsディレクトリを作成..."
mkdir -p results

# タスクを生成
echo "2. タスクを生成中..."
../build/taskorganizer create business_prompt.txt target_list.txt --clear

# .tasksディレクトリの内容を確認
echo "3. 生成されたタスクを確認:"
ls -la .tasks/pending/

# タスク管理マスタを起動（バックグラウンド）
echo "4. タスク管理マスタを起動..."
../build/taskorganizer master &
MASTER_PID=$!

# マスタが起動するまで少し待つ
sleep 2

# タスクワーカーを起動（2つ起動してみる）
echo "5. タスクワーカーを起動..."
../build/taskorganizer worker localhost 34567 &
WORKER1_PID=$!

../build/taskorganizer worker localhost 34567 &
WORKER2_PID=$!

echo "タスク処理中..."
echo "Ctrl+Cで中断できます"

# マスタプロセスの終了を待つ
wait $MASTER_PID

echo "全てのタスクが完了しました！"

# 結果を確認
echo "6. 処理結果:"
echo "完了タスク:"
ls -la .tasks/done/ 2>/dev/null || echo "  なし"
echo "失敗タスク:"
ls -la .tasks/failed/ 2>/dev/null || echo "  なし"
echo "生成された要約:"
ls -la results/ 2>/dev/null || echo "  なし"

# ワーカープロセスをクリーンアップ（念のため）
kill $WORKER1_PID $WORKER2_PID 2>/dev/null
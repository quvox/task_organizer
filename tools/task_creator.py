#!/usr/bin/env python3
# -*- coding: utf-8 -*-
"""
タスク生成ツール

ドキュメント対応箇所：docs/タスク生成ツール.md

機能概要：
- 業務プロンプトとターゲットリストから、個別のタスクプロンプトを生成
- .tasks/ディレクトリ構造を作成
- 各ターゲットに対応するタスクプロンプトファイルを.tasks/pending/に出力

処理フロー：
1. コマンドライン引数の解析（業務プロンプトファイル、ターゲットリストファイル）
2. .tasksディレクトリ構造の作成
3. 業務プロンプトの読み込み
4. ターゲットリストの読み込み
5. 各ターゲットに対するタスクプロンプトの生成・出力
"""

import os
import sys
import argparse
from pathlib import Path


def create_task_directories(root_dir):
    """
    タスク管理用ディレクトリ構造を作成する
    
    ドキュメント対応箇所：docs/タスク生成ツール.md 15行目
    「タスク生成ツールを起動すると、ルートディレクトリの下に、.tasks/ディレクトリを生成し、
    さらにその下に、pending、working、done、failedというサブディレクトリを作る。」
    
    Args:
        root_dir (str): ルートディレクトリのパス
    """
    # ルートディレクトリ配下の.tasksディレクトリとその下のサブディレクトリを作成
    tasks_root = os.path.join(root_dir, ".tasks")
    task_dirs = [
        tasks_root,
        os.path.join(tasks_root, "pending"),
        os.path.join(tasks_root, "working"), 
        os.path.join(tasks_root, "done"),
        os.path.join(tasks_root, "failed")
    ]
    
    for dir_path in task_dirs:
        os.makedirs(dir_path, exist_ok=True)
        print(f"ディレクトリを作成しました: {dir_path}")


def read_business_prompt(prompt_file_path):
    """
    業務プロンプトファイルを読み込む
    
    ドキュメント対応箇所：docs/タスク生成ツール.md 7行目
    「業務プロンプトには、実行させたい業務がテキストで記述されている。」
    
    Args:
        prompt_file_path (str): 業務プロンプトファイルのパス
        
    Returns:
        str: 業務プロンプトの内容
        
    Raises:
        FileNotFoundError: ファイルが存在しない場合
        Exception: ファイル読み込みエラー
    """
    try:
        with open(prompt_file_path, 'r', encoding='utf-8') as f:
            content = f.read().strip()
        print(f"業務プロンプトを読み込みました: {prompt_file_path}")
        return content
    except FileNotFoundError:
        print(f"エラー: 業務プロンプトファイルが見つかりません: {prompt_file_path}")
        raise
    except Exception as e:
        print(f"エラー: 業務プロンプトファイルの読み込みに失敗しました: {e}")
        raise


def read_target_list(target_list_path):
    """
    ターゲットリストファイルを読み込む
    
    ドキュメント対応箇所：docs/タスク生成ツール.md 9行目
    「ターゲットリストには、業務対象のリストが改行区切りのテキストで記述されている。」
    
    Args:
        target_list_path (str): ターゲットリストファイルのパス
        
    Returns:
        list: ターゲットリストの各行（空行は除外）
        
    Raises:
        FileNotFoundError: ファイルが存在しない場合
        Exception: ファイル読み込みエラー
    """
    try:
        with open(target_list_path, 'r', encoding='utf-8') as f:
            lines = [line.strip() for line in f.readlines() if line.strip()]
        print(f"ターゲットリストを読み込みました: {target_list_path} ({len(lines)}件)")
        return lines
    except FileNotFoundError:
        print(f"エラー: ターゲットリストファイルが見つかりません: {target_list_path}")
        raise
    except Exception as e:
        print(f"エラー: ターゲットリストファイルの読み込みに失敗しました: {e}")
        raise


def generate_task_prompt(business_prompt, target_entry):
    """
    個別のタスクプロンプトを生成する
    
    ドキュメント対応箇所：docs/タスク生成ツール.md 17-21行目
    テンプレート：
    「あなたは、有能なビジネスマンです。上司から、「＜業務プロンプトの内容＞」という
    ミッションを与えられました。一緒に与えられたリストを複数のビジネスマンが分担して
    取り組みます。あなたが割り当てられたのは、＜ターゲットリストのエントリ＞です。
    上司からの指示に従ってミッションを実行してください。」
    
    Args:
        business_prompt (str): 業務プロンプトの内容
        target_entry (str): ターゲットリストの1エントリ
        
    Returns:
        str: 生成されたタスクプロンプト
    """
    # ドキュメントで指定されたテンプレートに従ってタスクプロンプトを生成
    task_prompt = f"""あなたは、有能なビジネスマンです。上司から、「{business_prompt}」というミッションを与えられました。一緒に与えられたリストを複数のビジネスマンが分担して取り組みます。あなたが割り当てられたのは、{target_entry}です。上司からの指示に従ってミッションを実行してください。"""
    
    return task_prompt


def write_task_prompt_file(task_prompt, target_entry, index, root_dir):
    """
    タスクプロンプトをファイルに出力する
    
    ドキュメント対応箇所：docs/タスク生成ツール.md 17行目
    「タスク生成ツールは、ターゲットリスト1行1行に対して、タスクプロンプトを生成し、
    .tasks/pending/の下にテキストファイルとして出力する。」
    
    Args:
        task_prompt (str): タスクプロンプトの内容
        target_entry (str): ターゲットエントリ（ファイル名生成用）
        index (int): インデックス番号（ファイル名生成用）
        root_dir (str): ルートディレクトリのパス
        
    Returns:
        str: 作成されたファイルのパス
    """
    # ファイル名の生成（安全な文字のみ使用）
    # ターゲットエントリから不正な文字を除去してファイル名にする
    safe_target = "".join(c for c in target_entry if c.isalnum() or c in "._-")[:50]
    filename = f"task_{index:03d}_{safe_target}.txt"
    file_path = os.path.join(root_dir, ".tasks", "pending", filename)
    
    try:
        with open(file_path, 'w', encoding='utf-8') as f:
            f.write(task_prompt)
        print(f"タスクプロンプトファイルを作成しました: {file_path}")
        return file_path
    except Exception as e:
        print(f"エラー: タスクプロンプトファイルの作成に失敗しました: {e}")
        raise


def main():
    """
    メイン処理
    
    ドキュメント対応箇所：docs/タスク生成ツール.md 5-7行目
    「タスク生成ツールには、2つのテキストファイルパスを引数で与える。
    一つは業務プロンプトテキスト、もう一つはターゲットリストである。
    タスク生成ツールには、さらにオプショナル引数でルートディレクトリパスを指定する。
    なお、デフォルトのルートディレクトリはtoolsの一つ上のディレクトリとする。」
    """
    # コマンドライン引数の解析
    parser = argparse.ArgumentParser(
        description="タスク生成ツール - 業務プロンプトとターゲットリストから個別タスクを生成",
        formatter_class=argparse.RawDescriptionHelpFormatter,
        epilog="""
使用例:
  python task_creator.py business_prompt.txt target_list.txt
  python task_creator.py business_prompt.txt target_list.txt --root-dir /path/to/root
  
引数:
  business_prompt_file: 業務プロンプトが記述されたテキストファイル
  target_list_file:     ターゲットリストが記述されたテキストファイル（改行区切り）
  
オプション:
  --root-dir:          ルートディレクトリのパス（デフォルト：カレントディレクトリ）
        """
    )
    
    parser.add_argument(
        'business_prompt_file',
        help='業務プロンプトファイルのパス'
    )
    
    parser.add_argument(
        'target_list_file', 
        help='ターゲットリストファイルのパス'
    )
    
    # ドキュメント対応箇所：docs/タスク生成ツール.md 7行目
    # 「さらにオプショナル引数でルートディレクトリパスを指定する。
    # なお、デフォルトのルートディレクトリはカレントディレクトリとする。」
    parser.add_argument(
        '--root-dir',
        default=os.getcwd(),
        help='ルートディレクトリのパス（デフォルト：カレントディレクトリ）'
    )
    
    args = parser.parse_args()
    
    try:
        print("=== タスク生成ツール開始 ===")
        print(f"業務プロンプトファイル: {args.business_prompt_file}")
        print(f"ターゲットリストファイル: {args.target_list_file}")
        print(f"ルートディレクトリ: {args.root_dir}")
        print()
        
        # Step 1: タスク管理用ディレクトリ構造を作成
        print("1. タスク管理用ディレクトリ構造を作成中...")
        create_task_directories(args.root_dir)
        print()
        
        # Step 2: 業務プロンプトを読み込み
        print("2. 業務プロンプトを読み込み中...")
        business_prompt = read_business_prompt(args.business_prompt_file)
        print(f"業務プロンプト: {business_prompt[:100]}{'...' if len(business_prompt) > 100 else ''}")
        print()
        
        # Step 3: ターゲットリストを読み込み
        print("3. ターゲットリストを読み込み中...")
        target_list = read_target_list(args.target_list_file)
        print(f"ターゲット数: {len(target_list)}件")
        print()
        
        # Step 4: 各ターゲットに対してタスクプロンプトを生成・出力
        print("4. タスクプロンプトを生成中...")
        created_files = []
        
        for index, target_entry in enumerate(target_list, 1):
            print(f"  処理中 ({index}/{len(target_list)}): {target_entry}")
            
            # タスクプロンプトを生成
            task_prompt = generate_task_prompt(business_prompt, target_entry)
            
            # ファイルに出力
            file_path = write_task_prompt_file(task_prompt, target_entry, index, args.root_dir)
            created_files.append(file_path)
        
        print()
        print("=== タスク生成完了 ===")
        print(f"生成されたタスク数: {len(created_files)}件")
        print(f"出力ディレクトリ: {os.path.join(args.root_dir, '.tasks', 'pending')}")
        print()
        print("次の手順:")
        print(f"1. タスク管理マスタを起動: python task_master.py --root-dir {args.root_dir}")
        print("2. タスクワーカーを起動: python task_worker.py")
        
    except Exception as e:
        print(f"エラー: {e}")
        sys.exit(1)


if __name__ == "__main__":
    main()
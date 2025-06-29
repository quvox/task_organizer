#!/usr/bin/env python3
"""
タスク管理ツールパッケージのセットアップスクリプト

このスクリプトは、タスク管理ツール群をPythonパッケージとして
インストール可能にするためのセットアップファイルです。

使用方法:
    pip install -e .     # 開発モードでインストール
    pip install .        # 通常インストール
    python setup.py sdist  # ソース配布用パッケージ作成
"""

from setuptools import setup, find_packages
import os
from pathlib import Path

# 現在のディレクトリ取得
here = Path(__file__).parent.absolute()

# README.mdからlong_descriptionを取得（存在する場合）
long_description = ""
readme_path = here / "README.md"
if readme_path.exists():
    with open(readme_path, encoding="utf-8") as f:
        long_description = f.read()

# requirements.txtから依存関係を取得（存在する場合）
requirements = []
requirements_path = here / "requirements.txt"
if requirements_path.exists():
    with open(requirements_path, encoding="utf-8") as f:
        requirements = [line.strip() for line in f if line.strip() and not line.startswith("#")]

setup(
    # パッケージ基本情報
    name="task-management-tools",
    version="0.1.0",
    author="Claude Code",
    author_email="noreply@anthropic.com",
    description="分散タスク実行システム - Claude APIを使用したタスク管理ツール群",
    long_description=long_description,
    long_description_content_type="text/markdown",
    url="https://github.com/anthropics/claude-code",
    project_urls={
        "Bug Reports": "https://github.com/anthropics/claude-code/issues",
        "Documentation": "https://docs.anthropic.com/en/docs/claude-code",
    },
    
    # パッケージ構成
    packages=find_packages(),
    py_modules=[],
    include_package_data=True,
    
    # Pythonバージョン要件
    python_requires=">=3.8",
    
    # 依存関係
    install_requires=requirements,
    
    # 開発時の依存関係
    extras_require={
        "dev": [
            "pytest>=6.0",
            "pytest-cov",
            "black",
            "flake8",
            "mypy",
        ]
    },
    
    # コンソールコマンドとしてのエントリポイント
    entry_points={
        "console_scripts": [
            "task-master=tools.task_master:main",
            "task-worker=tools.task_worker:main",
            "task-creator=tools.task_creator:main",
        ],
    },
    
    # パッケージ分類
    classifiers=[
        "Development Status :: 3 - Alpha",
        "Intended Audience :: Developers",
        "Topic :: Software Development :: Build Tools",
        "License :: OSI Approved :: MIT License",
        "Programming Language :: Python :: 3",
        "Programming Language :: Python :: 3.8",
        "Programming Language :: Python :: 3.9",
        "Programming Language :: Python :: 3.10",
        "Programming Language :: Python :: 3.11",
        "Programming Language :: Python :: 3.12",
        "Operating System :: OS Independent",
    ],
    
    # キーワード
    keywords="task management distributed claude api automation",
    
    # ライセンス
    license="MIT",
    
    # zipセーフかどうか
    zip_safe=False,
)
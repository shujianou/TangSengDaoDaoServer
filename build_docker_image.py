#!/usr/bin/env python3
# -*- coding: utf-8 -*-

import os
import shutil
import subprocess
import sys
from pathlib import Path

def check_docker():
    """检查 Docker 是否可用"""
    try:
        subprocess.run(["docker", "--version"], capture_output=True, check=True)
        return True
    except (subprocess.CalledProcessError, FileNotFoundError):
        print("\n错误: Docker 未安装或未运行")
        print("请确保：")
        print("1. Docker Desktop 已安装")
        print("2. Docker Desktop 正在运行")
        print("3. WSL2 集成已在 Docker Desktop 设置中启用")
        print("\n详细信息请访问: https://docs.docker.com/go/wsl2/")
        return False

def copy_directory_contents(src, dst):
    """复制目录内容到目标目录，排除目标目录本身"""
    if not os.path.exists(dst):
        os.makedirs(dst)
    
    for item in os.listdir(src):
        if item == os.path.basename(dst):
            continue
        s = os.path.join(src, item)
        d = os.path.join(dst, item)
        if os.path.isdir(s):
            shutil.copytree(s, d, dirs_exist_ok=True)
        else:
            shutil.copy2(s, d)

def main():
    # 检查 Docker 是否可用
    if not check_docker():
        sys.exit(1)

    # 设置基本变量
    VERSION = "1.5.0-SNAPSHOT"
    REGISTRY = "registry.cn-shenzhen.aliyuncs.com"
    IMAGE_NAME = "golfonline-im-biz"

    # 选择环境
    print("请选择构建环境:")
    print("[1] 本地环境")
    print("[2] 开发环境")
    
    choice = input("请输入选择 (1/2) [1]: ").strip() or "1"
    
    CONFIG_DIR = "dev-configs" if choice == "2" else "configs"

    # 创建和清理临时构建目录
    build_temp = "build_temp"
    if os.path.exists(build_temp):
        shutil.rmtree(build_temp)
    os.makedirs(build_temp)

    # 使用改进的目录复制方法
    print("正在复制项目文件...")
    current_dir = os.getcwd()
    copy_directory_contents(current_dir, build_temp)

    # 复制 TangSengDaoDaoServerLib 到临时目录
    server_lib_path = "../TangSengDaoDaoServerLib"
    if os.path.exists(server_lib_path):
        print("正在复制 TangSengDaoDaoServerLib...")
        shutil.copytree(server_lib_path, f"{build_temp}/TangSengDaoDaoServerLib", dirs_exist_ok=True)

    print(f"使用 {CONFIG_DIR} 目录构建镜像...")
    
    try:
        # 构建 Docker 镜像
        image_tag = f"{REGISTRY}/golfonline-cloud/{IMAGE_NAME}:{VERSION}"
        build_cmd = f"docker build -t {image_tag} --build-arg CONFIG_DIR={CONFIG_DIR} ."
        subprocess.run(build_cmd, shell=True, check=True)

        # 询问是否推送镜像
        push = input("是否要推送镜像到远程仓库? (Y/N) [Y]: ").strip().upper() or "Y"
        
        if push != "N":
            print("正在登录阿里云镜像仓库...")
            login_cmd = f"docker login {REGISTRY} -u mse@greenjoy -p Htx2022!"
            subprocess.run(login_cmd, shell=True, check=True)

            print("正在推送镜像...")
            push_cmd = f"docker push {image_tag}"
            subprocess.run(push_cmd, shell=True, check=True)
            print("镜像推送完成")
        else:
            print("取消推送镜像")

    except subprocess.CalledProcessError as e:
        print(f"\n错误: 执行命令失败: {e}")
        sys.exit(1)
    finally:
        # 清理临时目录
        print("清理临时文件...")
        shutil.rmtree(build_temp)

if __name__ == "__main__":
    main() 
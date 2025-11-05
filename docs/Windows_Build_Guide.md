# Windows 构建指南

本文档介绍如何在 Windows 环境下构建 bililive-go 项目。

## 环境准备

Windows 系统需要模拟 Unix 环境才能使用 Makefile 构建。以下是几种选择：

- Git Bash (推荐)
- MSYS2
- Cygwin

## 构建步骤

### 1. 安装 make

使用 Chocolatey 安装 make：

```bash
choco install make
```

### 2. 构建前端资源

```bash
# 进入 webapp 目录
cd src/webapp

# 安装前端依赖并构建
yarn install
yarn build
```

### 3. 构建整个项目



```bash
# 在项目根目录中执行
make
```

### 4. 完成

构建完成后，可执行文件将位于 `bin` 目录中。

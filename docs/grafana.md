# 三合一简化安装，原本的文档太晦涩了。

## 安装
1. clone repo
>$ git clone https://github.com/bililive-go/bililive-go.git

2. 编辑 .env
自定义账户密码，将example.env保存为.env
默认账户密码 `admin admin`
不改网页里也会提醒你去改。

3. 命令行输入
>$ docker compose up

假如docker 版本低于20，需要安装docker-compose。新的版本是直接内置的

4. 浏览器打开 http://localhost:3000 。

tips
- 使用默认端口和别的端口冲突时，修改相关ports:
- `./Videos:/srv/bililive` 默认保存路径需要自定义


bibliography
1. [Docker-Compose-Prometheus-and-Grafana](https://github.com/Einsteinish/Docker-Compose-Prometheus-and-Grafana) 

# 手动安装笔记
没有 docker 或者想在其他机器配置监控也可以选择手动安装。
虽然API.md里面没有写，但是路径应该是`/api/metrics` 。`prometheus.yml` 需要写成以下形式
``` yml
global:
  scrape_interval: 15s
scrape_configs:
  - job_name: "bililive"
    metrics_path: "/api/metrics"
    scheme: http
    static_configs:
      - targets: ["bililive-go:8080"] #自行修改ip端口
```
grafana 需要打开浏览器，然后复制[面板内容](/contrib/grafana/dashboard.json)导入

# 群晖（Synology）的情况
[启用 grafana 统计面板](./Synology-related.md#启用-grafana-统计面板)
# Bililive-go
[![CI](https://github.com/hr3lxphr6j/bililive-go/actions/workflows/tests.yaml/badge.svg?branch=master)](https://github.com/hr3lxphr6j/bililive-go/actions/workflows/tests.yaml)
[![Go Report Card](https://goreportcard.com/badge/github.com/hr3lxphr6j/bililive-go)](https://goreportcard.com/report/github.com/hr3lxphr6j/bililive-go)
[![Github release](https://img.shields.io/github/release/hr3lxphr6j/bililive-go.svg)](https://github.com/hr3lxphr6j/bililive-go/releases/latest)
[![Docker Pulls](https://img.shields.io/docker/pulls/chigusa/bililive-go.svg)](https://hub.docker.com/r/chigusa/bililive-go/)
[![Bilibili](https://img.shields.io/badge/%E5%93%94%E5%93%A9%E5%93%94%E5%93%A9-%E6%9C%AA%E6%9D%A5%E7%A7%91%E6%8A%80%E7%8E%8B%E8%80%81%E8%8F%8A%E5%BD%95%E6%92%AD%E7%BB%84-ebb8d0.svg)](https://space.bilibili.com/18578203/)

Bililive-go是一个支持多种直播平台的直播录制工具   

![image](https://github.com/hr3lxphr6j/bililive-go/raw/master/docs/screenshot.webp)

## 支持网站

<table>
    <tr align="center">
        <th>站点</th>
        <th>url</th>
        <th>支持情况</th>
    </tr>
    <tr align="center">
        <td>Acfun直播</td>
        <td>live.acfun.cn</td>
        <td>滋瓷</td>
    </tr>
    <tr align="center">
        <td>哔哩哔哩直播</td>
        <td>live.bilibili.com</td>
        <td>滋瓷</td>
    </tr>
    <tr align="center">
        <td>战旗直播</td>
        <td>www.zhanqi.tv</td>
        <td>滋瓷</td>
    </tr>
    <tr align="center">
        <td>斗鱼直播</td>
        <td>www.douyu.com</td>
        <td>滋瓷</td>
    </tr>
    <tr align="center">
        <td>火猫直播</td>
        <td>www.huomao.com</td>
        <td>滋瓷</td>
    </tr>
    <tr align="center">
        <td>龙珠直播</td>
        <td>longzhu.com</td>
        <td>滋瓷</td>
    </tr>
    <tr align="center">
        <td>虎牙直播</td>
        <td>www.huya.com</td>
        <td>滋瓷</td>
    </tr>
    <tr align="center">
        <td>CC直播</td>
        <td>cc.163.com</td>
        <td>滋瓷</td>
    </tr>
    <tr align="center">
        <td>一直播</td>
        <td>www.yizhibo.com</td>
        <td>滋瓷</td>
    </tr>
    <tr align="center">
        <td>twitch</td>
        <td>www.twitch.tv</td>
        <td>滋瓷</td>
    </tr>
    <tr align="center">
        <td>OPENREC</td>
        <td>www.openrec.tv</td>
        <td>滋瓷</td>
    </tr>
    <tr align="center">
        <td>企鹅电竞</td>
        <td>egame.qq.com</td>
        <td>滋瓷</td>
    </tr>
    <tr align="center">
        <td>浪live</td>
        <td>play.lang.live & www.lang.live</td>
        <td>滋瓷</td>
    </tr>
    <tr align="center">
        <td>花椒</td>
        <td>www.huajiao.com</td>
        <td>滋瓷</td>
    </tr>
    <tr align="center">
        <td>抖音直播</td>
        <td>live.douyin.com</td>
        <td>TODO</td>
    </tr>
</table>

## Grafana 面板

> 请自行部署 prometheus 和 grafana

![image](https://github.com/hr3lxphr6j/bililive-go/raw/master/docs/dashboard.webp)

## 依赖
* [ffmpeg](https://ffmpeg.org/)

## 使用例子
- 本地
    ```
    ./bililive-go -i https://live.bilibili.com/1030 -i https://www.douyu.com/6655
    ```
- docker
    ```
    docker run --restart=always -v ~/Videos:/srv/bililive -p 8080:8080 -d chigusa/bililive-go
    ```

## 获取&编译
```
$ go get github.com/hr3lxphr6j/bililive-go
$ cd $GOPATH/src/github.com/hr3lxphr6j/bililive-go
$ make
```

## Windows构建
请参考[Windows构建指南](docs/Windows_Build_Guide.md)

## Wiki
[Wiki](https://github.com/hr3lxphr6j/bililive-go/wiki)

## API
[API doc](https://github.com/hr3lxphr6j/bililive-go/blob/master/docs/API.md)

## 参考
- [you-get](https://github.com/soimort/you-get)
- [ykdl](https://github.com/zhangn1985/ykdl)
- [youtube-dl](https://github.com/ytdl-org/youtube-dl)

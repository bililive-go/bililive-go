# Bililive-go API

## `GET /api/info` Get app info
- Request:
    ```text
    method: GET
    path: http://127.0.0.1:8080/api/info
    ```
- Response:
    ```json
    {
      "app_name": "BiliLive-go",
      "app_version": "0.5.0-rc.3-3-g31ceeda",
      "build_time": "2020-05-05_01:07:16",
      "git_hash": "31ceeda8f508ba5546cfdefef5f3945828a87651",
      "pid": 33295,
      "platform": "darwin/amd64",
      "go_version": "go1.14.2"
    }
    ```
        
## `GET /api/lives` Get all live info 
- Request:  
    ```text
    method: GET
    path: http://127.0.0.1:8080/api/lives
    ```
- Response:   
    ```json
    [
      {
        "id": "212d9c98c7b376b730d4336bb49f6d3f",
        "live_url": "https://live.bilibili.com/14917277",
        "platform_cn_name": "哔哩哔哩",
        "host_name": "湊-阿库娅Official",
        "room_name": "【B站限定】棉花糖＆唱歌！！！！",
        "status": false,
        "listening": true,
        "recording": false
      },
      {
        "id": "63dc965c77d3d81058c92c3e38822256",
        "live_url": "https://live.bilibili.com/11588230",
        "platform_cn_name": "哔哩哔哩",
        "host_name": "白上吹雪Official",
        "room_name": "古老niconico老人会with☆乐园",
        "status": false,
        "listening": true,
        "recording": false
      },
      {
        "id": "dfb964a56725bbad165cb9ea1ef8ac5b",
        "live_url": "https://live.bilibili.com/1030",
        "platform_cn_name": "哔哩哔哩",
        "host_name": "怕上火暴王老菊",
        "room_name": "直播做饭",
        "status": false,
        "listening": true,
        "recording": false
      }
    ]
    ```
        
## `GET /api/lives/{id}` Get live info by id
- Request:  
    ```text
    method: GET
    path: http://127.0.0.1:8080/api/lives/212d9c98c7b376b730d4336bb49f6d3f
    ```
- Response:
    ```json
    {
      "id": "212d9c98c7b376b730d4336bb49f6d3f",
      "live_url": "https://live.bilibili.com/14917277",
      "platform_cn_name": "哔哩哔哩",
      "host_name": "湊-阿库娅Official",
      "room_name": "【B站限定】棉花糖＆唱歌！！！！",
      "status": false,
      "listening": true,
      "recording": false
    }
    ```
        
## `POST /api/lives` Add live
- Request:  
    ```text
    method: POST
    path: http://127.0.0.1:8080/api/lives
    body: 
        [
            {
                "url": "https://live.bilibili.com/14917277",
                "listen": true
            }
        ]
    ```
- Response:
    ```json
    [
        {
            "id": "212d9c98c7b376b730d4336bb49f6d3f",
            "live_url": "https://live.bilibili.com/14917277",
            "platform_cn_name": "哔哩哔哩",
            "host_name": "湊-阿库娅Official",
            "room_name": "【B站限定】棉花糖＆唱歌！！！！",
            "status": false,
            "listening": true,
            "recording": false
        }
    ]
    ```        
        
## `DELETE /api/lives/{id}` Delete live by id
- Request:  
    ```text
    method: DELETE
    path: http://127.0.0.1:8080/api/lives/212d9c98c7b376b730d4336bb49f6d3f
    ```
- Response:
    ```json
    {
        "err_no": 0,
        "err_msg": "",
        "data": "OK"
    }
    ```

## `GET /api/lives/{id}/start` Start listen live by id
- Request:  
    ```text
    method: GET
    path: http://127.0.0.1:8080/api/lives/212d9c98c7b376b730d4336bb49f6d3f/start
    ```
- Response:
    ```json
    {
        "id": "212d9c98c7b376b730d4336bb49f6d3f",
        "live_url": "https://live.bilibili.com/14917277",
        "platform_cn_name": "哔哩哔哩",
        "host_name": "湊-阿库娅Official",
        "room_name": "【B站限定】棉花糖＆唱歌！！！！",
        "status": false,
        "listening": true,
        "recording": false
    }
    ```
        
## `GET /api/lives/{id}/stop` Stop listen and record live by id
- Request:  
    ```text
    method: GET
    path: http://127.0.0.1:8080/api/lives/212d9c98c7b376b730d4336bb49f6d3f/stop
    ```
- Response:
    ```json
    {
        "id": "212d9c98c7b376b730d4336bb49f6d3f",
        "live_url": "https://live.bilibili.com/14917277",
        "platform_cn_name": "哔哩哔哩",
        "host_name": "湊-阿库娅Official",
        "room_name": "【B站限定】棉花糖＆唱歌！！！！",
        "status": false,
        "listening": false,
        "recording": false
    }
    ```
        
## `GET /api/lives/{id}/stream-urls` Get stream URLs for live room
- Request:  
    ```text
    method: GET
    path: http://127.0.0.1:8080/api/lives/212d9c98c7b376b730d4336bb49f6d3f/stream-urls
    ```
- Response (Success):
    ```json
    {
        "stream_urls": [
            {
                "name": "高清",
                "description": "FLV Stream - 高清",
                "url": "http://example.com/stream1.flv",
                "resolution": 720,
                "vbitrate": 2500000
            },
            {
                "name": "原画",
                "description": "FLV Stream - 原画",
                "url": "http://example.com/stream2.flv", 
                "resolution": 1080,
                "vbitrate": 6000000
            }
        ],
        "platform": "哔哩哔哩"
    }
    ```
- Response (Room not monitoring):
    ```json
    {
        "err_no": 400,
        "err_msg": "直播间未在监控中，无法获取直播源",
        "data": null
    }
    ```
- Response (Stream not available):
    ```json
    {
        "err_no": 503,
        "err_msg": "无法获取直播源URL，直播可能已结束或出现错误",
        "data": null
    }
    ```

## `GET /api/config` Get config info
- Request:  
    ```text
    method: GET
    path: http://127.0.0.1:8080/api/config
    ```
- Response:
    ```json
    {
      "RPC": {
        "Enable": true,
        "Bind": "127.0.0.1:8080"
      },
      "Debug": false,
      "Interval": 15,
      "OutPutPath": "/tmp",
      "Feature": {
        "UseNativeFlvParser": false
      },
      "LiveRooms": null
    }
    ```
        
## `PUT /api/config` Save lives info to config file
- Request:  
    ```text
    method: PUT
    path: http://127.0.0.1:8080/api/config
    ```
- Response:
    ```json
    {
        "err_no": 0,
        "err_msg": "",
        "data": "OK"
    }
    ```

## `GET /api/raw-config` Get raw config file
- Request:
    ```text
    method: GET
    path: http://127.0.0.1:8080/api/raw-config
    ```
- Response:
    ```json
    {
        "config": "rpc:\n  enable: true\n  bind: 0.0.0.0:8080\ndebug: false\ninterval: 15\nout_put_path: ./\nfeature:\n  use_native_flv_parser: false\nlive_rooms:\n- url: https://www.huya.com/991111\n  is_listening: false\nout_put_tmpl: \"\"\nvideo_split_strategies:\n  on_room_name_changed: false\n  max_duration: 0s\ncookies:\n  live.douyin.com: name1=qwer;name2=asdf;aaaa\non_record_finished:\n  convert_to_mp4: true\n  delete_flv_after_convert: false\ntimeout_in_us: 50000000\n"
    }
    ```

## `PUT /api/raw-config` Save the whole config file
- Request:
    ```text
    method: PUT
    path: http://127.0.0.1:8080/api/raw-config
    body:
        {
            "config": "rpc:\n  enable: true\n  bind: 0.0.0.0:8080\ndebug: false\ninterval: 15\nout_put_path: ./\nfeature:\n  use_native_flv_parser: false\nlive_rooms:\n- url: https://www.huya.com/991111\n  is_listening: false\nout_put_tmpl: \"\"\nvideo_split_strategies:\n  on_room_name_changed: false\n  max_duration: 0s\ncookies:\n  live.douyin.com: name1=qwer;name2=asdf;aaaa\non_record_finished:\n  convert_to_mp4: true\n  delete_flv_after_convert: false\ntimeout_in_us: 50000000\n"
        }
    ```
- Response:
    ```json
    {
        "err_no": 0,
        "err_msg": "",
        "data": "OK"
    }
    ```
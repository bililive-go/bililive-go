# NFO 和海报生成功能

本功能为 bililive-go 添加了自动生成 NFO 元数据文件和视频海报的能力，方便在 Emby、Jellyfin 等媒体服务器中使用。

## 功能特性

### NFO 文件生成
- 自动为录制的视频文件生成 NFO 元数据文件
- 兼容 Emby 和 Jellyfin 媒体服务器格式
- 包含主播名称、直播间名称、平台信息等中文元数据
- 自动计算视频时长信息

### 海报图片生成  
- 使用 FFmpeg 从视频中提取指定时间点的帧作为海报
- 支持自定义提取时间点
- 生成高质量 JPEG 格式图片
- 自动命名与视频文件保持一致

## 配置选项

在 `config.yml` 文件的 `on_record_finished` 部分添加以下配置：

```yaml
on_record_finished:
  convert_to_mp4: false
  delete_flv_after_convert: false
  # 启用 NFO 元数据文件生成
  generate_nfo: true
  # 启用海报图片生成  
  generate_poster: true
  # 海报生成时间点 (格式: HH:MM:SS)
  poster_time: "00:00:30"
  custom_commandline: ""
```

### 配置说明

- `generate_nfo`: 是否生成 NFO 文件 (默认: false)
- `generate_poster`: 是否生成海报图片 (默认: false) 
- `poster_time`: 海报提取的时间点，格式为 "HH:MM:SS" (默认: "00:00:30")

## 生成的文件

对于名为 `直播录制.flv` 的录制文件，将生成：

1. **NFO 文件**: `直播录制.nfo`
   - XML 格式的元数据文件
   - 包含视频标题、描述、时长等信息
   - 支持中文字符和特殊符号转义

2. **海报图片**: `直播录制.jpg`
   - JPEG 格式的封面图片
   - 从指定时间点提取的视频帧
   - 高质量输出适合媒体服务器显示

## NFO 文件示例

```xml
<?xml version="1.0" encoding="utf-8" standalone="yes"?>
<movie>
    <title>夜晚游戏直播间</title>
    <plot>直播录制：测试主播 - 夜晚游戏直播间

主播：测试主播
平台：Bilibili
录制时间：2025-09-27 00:30:00</plot>
    <tagline>直播录制</tagline>
    <year>2025</year>
    <premiered>2025-09-27</premiered>
    <runtime>120</runtime>
    <director>测试主播</director>
    <studio>Bilibili</studio>
    <genre>直播</genre>
    <genre>录播</genre>
    <country>CN</country>
    <language>zh</language>
</movie>
```

## 依赖要求

- **FFmpeg**: 用于海报图片生成，需要在系统 PATH 中可用或通过 `ffmpeg_path` 配置指定
- 生成功能会在录制完成后的后处理阶段执行

## 工作流程

1. 直播录制完成
2. 执行配置的后处理操作 (如格式转换)
3. 如果启用，生成 NFO 文件
4. 如果启用，使用 FFmpeg 生成海报图片
5. 所有文件保存在与原始录制文件相同的目录中

## 注意事项

- NFO 和海报生成是可选功能，默认禁用
- 生成失败不会影响录制文件本身
- 海报生成需要 FFmpeg 可用，如果失败会记录错误日志
- 文件名会自动转义特殊字符以确保兼容性
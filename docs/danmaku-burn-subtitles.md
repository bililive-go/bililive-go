# 弹幕字幕烧录（GPU 编码器）

弹幕字幕烧录功能可将 ASS 弹幕字幕硬编码（烧录）到视频画面中。烧录阶段基于 FFmpeg 实现，
本文将介绍烧录的编码器选项、质量参数与编码预设的联动规则。

## 配置字段

| 字段 | 说明 | 默认值 |
|------|------|--------|
| `burn_subtitles` | 是否开启字幕烧录 | `false` |
| `burn_subtitles_codec` | 视频编码器，可选 `libx264` / `libx265` / `h264_nvenc` / `hevc_nvenc` / `av1_nvenc` | `libx264` |
| `burn_subtitles_crf` | 质量值（软编码为 CRF，NVENC 为 CQ），越小画质越好；NVENC 的 `0` 表示自动质量；留空或空白时使用默认值 | `18` |
| `burn_subtitles_preset` | 编码预设（与编码器联动，见下文）；留空或空白时使用默认值 | `medium` |
| `burn_delete_ass` | 烧录成功后删除 ASS 文件 | `false` |
| `burn_delete_source` | 烧录成功后删除源视频（仅保留 MKV） | `false` |

> 这些字段属于 `on_record_finished` 配置块，可在 Web 管理界面「弹幕配置 → 字幕烧录设置」中调整，
> 也可在配置文件 `on_record_finished` 下直接填写。

## 视频编码器

- **libx264（默认）**：H.264 软件编码，兼容性最好，无需额外硬件。
- **libx265**：H.265 软件编码，压缩率更高（文件更小），但编码速度较慢。
- **h264_nvenc / hevc_nvenc / av1_nvenc**：NVIDIA 硬件编码，编码速度快、CPU 占用低，需要满足以下条件：
  - 使用 NVIDIA 显卡且已安装显卡驱动；
  - 当前 FFmpeg 构建包含对应 NVENC 编码器（可通过 `ffmpeg -encoders | grep nvenc` 验证）；
  - 显卡支持对应的 NVENC 会话（GTX 10 系列及以上基本支持 h264/hevc，AV1 需 RTX 40 系及以上）。

后端按"编码器名称包含 `nvenc`"识别 NVENC 硬件编码器，Web 界面采用相同逻辑，
因此通过配置文件或 API 写入的其他 NVENC 编码器名称在界面上同样按 NVENC 处理。

## 质量参数（CRF / CQ）

质量值与编码器联动：

- 使用 **libx264 / libx265** 时，质量值对应 FFmpeg 的 `-crf` 参数，取值 0-51（`0` 即无损，越高画质越差）；
- 使用 **NVENC 硬件编码**时，NVENC 编码器不支持 CRF，后端自动改传恒定质量参数
  `-rc:v vbr -cq <值> -b:v 0 -preset <档位>`（`cq` 为 NVENC 的恒定质量参数，`-b:v 0` 表示
  码率由质量决定、不设上限）。

质量值留空或为空白（Web 界面表单清空、配置文件或 API 写入 `""`）时，后端会回退为默认值 `18`，
不会把空字符串传给 FFmpeg 生成无效的 `-crf ""` / `-cq ""` 参数；编码预设留空时同理回退为 `medium`。

NVENC 的 CQ 取值注意（以 `ffmpeg -h encoder=h264_nvenc` 的说明为准）：

- **手动质量有效范围为 1-51**，越小画质越好、文件越大，默认 18；
- **`0` 表示"自动质量"而非最高画质**：FFmpeg 对 `-cq` 的定义是 `0 means automatic`。
  后端检测到 CQ 为 0 时会省略 `-cq` 参数，由编码器自动决定质量；
  Web 界面的提示文案与此语义保持一致。

## 编码预设联动

不同编码器支持不同的预设档位，Web 界面会随编码器自动切换可选项，并在切换编码器时把
不兼容的预设重置为当前编码器的默认档位：

| 编码器 | 预设 | 默认 |
|--------|------|------|
| libx264 / libx265 | `ultrafast` → `veryslow`（越慢画质越好、文件越小） | `medium` |
| NVENC 系列（h264_nvenc / hevc_nvenc / av1_nvenc 等） | `p1` → `p7`（p1 最快、p7 画质最好，常用 p5） | `p5` |

> 通过 API 或配置文件直接写入不兼容的预设组合时，后端不会静默丢弃参数，
> 请按照上表保持编码器与预设匹配，否则可能触发 FFmpeg 编码错误。

## 注意事项

- 烧录属于录制完成后的处理阶段，需要先开启弹幕录制（`danmaku_enable`）并生成 ASS 字幕文件；
- 输出的视频格式为 MKV（`.mkv`），原视频默认保留，可通过 `burn_delete_source` 删除；
- 指定 NVENC 系列编码器时请确认 FFmpeg 支持该编码器且显卡硬件支持，否则烧录阶段会报错。
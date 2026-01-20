package flv

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"runtime/debug"
	"sync"

	"github.com/bililive-go/bililive-go/src/configs"
	"github.com/bililive-go/bililive-go/src/instance"
	"github.com/bililive-go/bililive-go/src/live"
	"github.com/bililive-go/bililive-go/src/pkg/livelogger"
	"github.com/bililive-go/bililive-go/src/pkg/parser"
	"github.com/bililive-go/bililive-go/src/pkg/reader"
	"github.com/bililive-go/bililive-go/src/pkg/utils"
)

const (
	Name = "native"

	audioTag  uint8 = 8
	videoTag  uint8 = 9
	scriptTag uint8 = 18

	ioRetryCount int = 3
)

var (
	flvSign = []byte{0x46, 0x4c, 0x56, 0x01} // flv version01

	ErrNotFlvStream = errors.New("not flv stream")
	ErrUnknownTag   = errors.New("unknown tag")
)

func init() {
	parser.Register(Name, new(builder))
}

type builder struct{}

func (b *builder) Build(cfg map[string]string, logger *livelogger.LiveLogger) (parser.Parser, error) {
	audioOnly := cfg["audio_only"] == "true"
	return &Parser{
		Metadata:  Metadata{},
		hc:        &http.Client{},
		stopCh:    make(chan struct{}),
		closeOnce: new(sync.Once),
		audioOnly: audioOnly,
		logger:    logger,
	}, nil
}

type Metadata struct {
	HasVideo, HasAudio bool
}

type Parser struct {
	Metadata Metadata

	i              *reader.BufferedReader
	o              io.Writer
	avcHeaderCount uint8
	tagCount       uint32

	hc        *http.Client
	stopCh    chan struct{}
	closeOnce *sync.Once
	audioOnly bool
	logger    *livelogger.LiveLogger
}

func (p *Parser) ParseLiveStream(ctx context.Context, streamUrlInfo *live.StreamUrlInfo, live live.Live, file string) error {
	// 检查是否配置了分段策略，原生 FLV 解析器不支持
	cfg := configs.GetCurrentConfig()
	if cfg != nil {
		if cfg.VideoSplitStrategies.MaxDuration > 0 || cfg.VideoSplitStrategies.MaxFileSize > 0 {
			p.logger.Warn("原生 FLV 解析器不支持 max_duration 和 max_file_size 分段功能，这些设置将被忽略。如需分段功能，请使用 FFmpeg 或 BililiveRecorder 下载器。")
		}
	}

	url := streamUrlInfo.Url
	// init input
	req, err := http.NewRequest("GET", url.String(), nil)
	if err != nil {
		return err
	}
	req.Header.Add("User-Agent", "Chrome/59.0.3071.115")
	// add headers for downloader from live
	for k, v := range streamUrlInfo.HeadersForDownloader {
		req.Header.Set(k, v)
	}
	resp, err := p.hc.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	p.i = reader.New(resp.Body)
	defer p.i.Free()

	// init output
	f, err := os.OpenFile(file, os.O_RDWR|os.O_CREATE, 0666)
	if err != nil {
		return err
	}
	p.o = f
	defer f.Close()

	// start parse
	return p.doParse(ctx)
}

func (p *Parser) Stop() error {
	p.closeOnce.Do(func() {
		close(p.stopCh)
	})
	return nil
}

func (p *Parser) doParse(ctx context.Context) error {
	// header of flv
	b, err := p.i.ReadN(9)
	if err != nil {
		return err
	}
	// signature
	if !bytes.Equal(b[:4], flvSign) {
		return ErrNotFlvStream
	}
	// flag
	p.Metadata.HasVideo = uint8(b[4])&(1<<2) != 0
	p.Metadata.HasAudio = uint8(b[4])&1 != 0

	// offset must be 9
	if binary.BigEndian.Uint32(b[5:]) != 9 {
		return ErrNotFlvStream
	}

	// write flv header
	if err := p.doWrite(ctx, p.i.AllBytes()); err != nil {
		return err
	}
	p.i.Reset()

	for {
		select {
		case <-p.stopCh:
			return nil
		default:
			if err := p.parseTag(ctx); err != nil {
				return err
			}
		}
	}
}

func (p *Parser) doCopy(ctx context.Context, n uint32) error {
	if writtenCount, err := io.CopyN(p.o, p.i, int64(n)); err != nil || writtenCount != int64(writtenCount) {
		utils.PrintStack()
		if err == nil {
			err = fmt.Errorf("doCopy(%d), %d bytes written", n, writtenCount)
		}
		return err
	}
	return nil
}

func (p *Parser) doWrite(ctx context.Context, b []byte) error {
	_ = instance.GetInstance(ctx) // keep context link if needed
	logger := p.logger
	leftInputSize := len(b)
	for retryLeft := ioRetryCount; retryLeft > 0 && leftInputSize > 0; retryLeft-- {
		writtenCount, err := p.o.Write(b[len(b)-leftInputSize:])
		leftInputSize -= writtenCount
		if err != nil {
			logger.Debugf("%s", string(debug.Stack()))
			return err
		}
		if leftInputSize != 0 {
			logger.Debugf("doWrite() left %d bytes to write", leftInputSize)
		}
	}
	if leftInputSize != 0 {
		return fmt.Errorf("doWrite([%d]byte) tried %d times, but still has %d bytes to write", len(b), ioRetryCount, leftInputSize)
	}
	return nil
}

// Status 返回下载器的当前状态
func (p *Parser) Status() (map[string]interface{}, error) {
	return map[string]interface{}{
		"parser": Name,
	}, nil
}

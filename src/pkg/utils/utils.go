package utils

import (
	"context"
	"crypto/md5"
	"encoding/hex"
	"errors"
	"fmt"
	"math/rand"
	"net/url"
	"os"
	"os/exec"
	"regexp"
	"runtime/debug"
	"strings"

	"github.com/bililive-go/bililive-go/src/instance"
	"github.com/bililive-go/bililive-go/src/live"
	"github.com/sirupsen/logrus"
)

func init() {
	ConnCounterManager = ConnCounterManagerType{}
	ConnCounterManager.bcMap = make(map[string]*ByteCounter)
}

func GetFFmpegPath(ctx context.Context) (string, error) {
	path := instance.GetInstance(ctx).Config.FfmpegPath
	if path != "" {
		_, err := os.Stat(path)
		if err == nil {
			return path, nil
		} else {
			return "", err
		}
	}
	path, err := exec.LookPath("ffmpeg")
	if errors.Is(err, exec.ErrDot) {
		// put ffmpeg.exe and binary like bililive-windows-amd64.exe to the same folder is allowed
		path, err = exec.LookPath("./ffmpeg")
	}
	return path, err
}

func IsFFmpegExist(ctx context.Context) bool {
	_, err := GetFFmpegPath(ctx)
	return err == nil
}

func GetMd5String(b []byte) string {
	md5Obj := md5.New()
	md5Obj.Write(b)
	return hex.EncodeToString(md5Obj.Sum(nil))
}

var (
	lowercaseRunes = []rune("abcdefghijklmnopqrstuvwxyz")
	uppercaseRunes = []rune("ABCDEFGHIJKLMNOPQRSTUVWXYZ")
	lettersRunes   = append(lowercaseRunes, uppercaseRunes...)
	digitsRunes    = []rune("0123456789")
	allRunes       = append(lettersRunes, digitsRunes...)
)

func GenRandomName(n int) string {
	b := make([]rune, n)
	b[0] = lowercaseRunes[rand.Intn(len(lowercaseRunes))]
	for i := 1; i < n; i++ {
		b[i] = allRunes[rand.Intn(len(allRunes))]
	}
	return string(b)
}

func GenRandomString(length int, validChars string) string {
	b := make([]string, length)
	chars := strings.Split(validChars, "")
	for i := range b {
		b[i] = chars[rand.Intn(len(chars))]
	}
	return strings.Join(b, "")
}

func Match1(re, str string) string {
	reg, err := regexp.Compile(re)
	if err != nil {
		return ""
	}
	match := reg.FindStringSubmatch(str)
	if len(match) < 2 {
		return ""
	}
	return match[1]
}

func GenUrls(strs ...string) ([]*url.URL, error) {
	urls := make([]*url.URL, 0, len(strs))
	for _, str := range strs {
		u, err := url.Parse(str)
		if err != nil {
			return nil, err
		}
		urls = append(urls, u)
	}
	return urls, nil
}

func GenUrlInfos(urls []*url.URL, headersForDownloader map[string]string) []*live.StreamUrlInfo {
	infos := make([]*live.StreamUrlInfo, 0, len(urls))
	for _, u := range urls {
		infos = append(infos, &live.StreamUrlInfo{
			Url:                  u,
			Name:                 "",
			Description:          "",
			Resolution:           0,
			Vbitrate:             0,
			HeadersForDownloader: headersForDownloader,
		})
	}
	return infos
}

func PrintStack() {
	logrus.Debugf(string(debug.Stack()))
}

func ExecCommands(commands [][]string) error {
	pwd, err := os.Getwd()
	if err != nil {
		return err
	}
	for _, command := range commands {
		err := ExecCommandInDir(command, pwd)
		if err != nil {
			return err
		}
	}
	return nil
}

func ExecCommand(command []string) error {
	pwd, err := os.Getwd()
	if err != nil {
		return err
	}
	return ExecCommandInDir(command, pwd)
}

func ExecCommandsInDir(commands [][]string, dir string) error {
	for _, command := range commands {
		err := ExecCommandInDir(command, dir)
		if err != nil {
			return err
		}
	}
	return nil
}

func ExecCommandInDir(args []string, dir string) error {
	name := args[0]
	cmd := exec.Command(name, args[1:]...)
	cmd.Dir = dir
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	logrus.Info(cmd.String())
	return cmd.Run()
}

const (
	KB = 1024
	MB = 1024 * KB
	GB = 1024 * MB
)

func FormatBytes(bytes int64) string {
	switch {
	case bytes >= GB:
		return fmt.Sprintf("%.2f GB", float64(bytes)/float64(GB))
	case bytes >= MB:
		return fmt.Sprintf("%.2f MB", float64(bytes)/float64(MB))
	case bytes >= KB:
		return fmt.Sprintf("%.2f KB", float64(bytes)/float64(KB))
	default:
		return fmt.Sprintf("%d B", bytes)
	}
}

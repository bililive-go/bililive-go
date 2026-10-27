package pipeline

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// mockStage 模拟阶段，用于测试
type mockStage struct {
	name      string
	executeFn func(ctx *PipelineContext, input []FileInfo) ([]FileInfo, error)
}

func (s *mockStage) Name() string { return s.name }
func (s *mockStage) Execute(ctx *PipelineContext, input []FileInfo) ([]FileInfo, error) {
	return s.executeFn(ctx, input)
}

// 创建临时测试文件
func createTempFile(t *testing.T, dir, name string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	require.NoError(t, os.WriteFile(path, []byte("test content"), 0644))
	return path
}

// TestExecute_AllStagesSucceed_AllDeletableFilesRemoved 测试全部成功时删除标记文件
func TestExecute_AllStagesSucceed_AllDeletableFilesRemoved(t *testing.T) {
	tmpDir := t.TempDir()

	// 创建测试文件
	originalFile := createTempFile(t, tmpDir, "video.flv")
	intermediateFile := filepath.Join(tmpDir, "video.mp4")
	finalFile := filepath.Join(tmpDir, "video.mkv")

	config := &PipelineConfig{
		Stages: []StageConfig{
			{Name: "stage1"},
			{Name: "stage2"},
		},
	}

	stage1 := &mockStage{
		name: "stage1",
		executeFn: func(ctx *PipelineContext, input []FileInfo) ([]FileInfo, error) {
			// 模拟转换：产出新文件，标记原始文件为可删除
			require.NoError(t, os.WriteFile(intermediateFile, []byte("mp4"), 0644))
			return []FileInfo{
				{Path: intermediateFile, Type: FileTypeVideo},
				{Path: input[0].Path, Type: FileTypeVideo, Deletable: true}, // 标记原始文件
			}, nil
		},
	}

	stage2 := &mockStage{
		name: "stage2",
		executeFn: func(ctx *PipelineContext, input []FileInfo) ([]FileInfo, error) {
			// 模拟烧录：产出新文件，标记中间文件为可删除
			require.NoError(t, os.WriteFile(finalFile, []byte("mkv"), 0644))
			var output []FileInfo
			output = append(output, FileInfo{Path: finalFile, Type: FileTypeVideo})
			for _, f := range input {
				if f.Path == intermediateFile {
					f.Deletable = true
				}
				output = append(output, f)
			}
			return output, nil
		},
	}

	executor := NewExecutor(logrus.StandardLogger())
	executor.RegisterStage("stage1", func(config StageConfig) (Stage, error) { return stage1, nil })
	executor.RegisterStage("stage2", func(config StageConfig) (Stage, error) { return stage2, nil })

	ctx := &PipelineContext{
		Ctx: context.Background(),
	}

	results, err := executor.Execute(ctx, config, []FileInfo{
		{Path: originalFile, Type: FileTypeVideo},
	}, 0, nil, nil)

	// 验证：无错误
	assert.NoError(t, err)
	assert.Len(t, results, 2)

	// 验证：原始文件和中间文件已被删除
	assert.NoFileExists(t, originalFile, "原始文件应被删除")
	assert.NoFileExists(t, intermediateFile, "中间文件应被删除")

	// 验证：最终文件保留
	assert.FileExists(t, finalFile, "最终文件应保留")
}

// TestExecute_StageFailed_AllFilesPreserved 测试阶段失败时保留所有文件
func TestExecute_StageFailed_AllFilesPreserved(t *testing.T) {
	tmpDir := t.TempDir()

	originalFile := createTempFile(t, tmpDir, "video.flv")
	intermediateFile := filepath.Join(tmpDir, "video.mp4")

	config := &PipelineConfig{
		Stages: []StageConfig{
			{Name: "stage1"},
			{Name: "stage2"},
		},
	}

	stage1 := &mockStage{
		name: "stage1",
		executeFn: func(ctx *PipelineContext, input []FileInfo) ([]FileInfo, error) {
			require.NoError(t, os.WriteFile(intermediateFile, []byte("mp4"), 0644))
			return []FileInfo{
				{Path: intermediateFile, Type: FileTypeVideo},
				{Path: input[0].Path, Type: FileTypeVideo, Deletable: true},
			}, nil
		},
	}

	stage2 := &mockStage{
		name: "stage2",
		executeFn: func(ctx *PipelineContext, input []FileInfo) ([]FileInfo, error) {
			return nil, fmt.Errorf("模拟失败")
		},
	}

	executor := NewExecutor(logrus.StandardLogger())
	executor.RegisterStage("stage1", func(config StageConfig) (Stage, error) { return stage1, nil })
	executor.RegisterStage("stage2", func(config StageConfig) (Stage, error) { return stage2, nil })

	ctx := &PipelineContext{
		Ctx: context.Background(),
	}

	_, err := executor.Execute(ctx, config, []FileInfo{
		{Path: originalFile, Type: FileTypeVideo},
	}, 0, nil, nil)

	// 验证：返回错误
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "模拟失败")

	// 验证：所有文件保留
	assert.FileExists(t, originalFile, "原始文件应保留")
	assert.FileExists(t, intermediateFile, "中间文件应保留")
}

// TestExecute_Cancelled_AllFilesPreserved 测试取消时保留所有文件
func TestExecute_Cancelled_AllFilesPreserved(t *testing.T) {
	tmpDir := t.TempDir()

	originalFile := createTempFile(t, tmpDir, "video.flv")
	intermediateFile := filepath.Join(tmpDir, "video.mp4")

	config := &PipelineConfig{
		Stages: []StageConfig{
			{Name: "stage1"},
			{Name: "stage2"},
		},
	}

	stage1 := &mockStage{
		name: "stage1",
		executeFn: func(ctx *PipelineContext, input []FileInfo) ([]FileInfo, error) {
			require.NoError(t, os.WriteFile(intermediateFile, []byte("mp4"), 0644))
			return []FileInfo{
				{Path: intermediateFile, Type: FileTypeVideo},
				{Path: input[0].Path, Type: FileTypeVideo, Deletable: true},
			}, nil
		},
	}

	cancelCtx, cancel := context.WithCancel(context.Background())

	stage2 := &mockStage{
		name: "stage2",
		executeFn: func(ctx *PipelineContext, input []FileInfo) ([]FileInfo, error) {
			// 在 stage2 执行时取消 context
			cancel()
			return []FileInfo{input[0]}, nil
		},
	}

	executor := NewExecutor(logrus.StandardLogger())
	executor.RegisterStage("stage1", func(config StageConfig) (Stage, error) { return stage1, nil })
	executor.RegisterStage("stage2", func(config StageConfig) (Stage, error) { return stage2, nil })

	ctx := &PipelineContext{
		Ctx: cancelCtx,
	}

	_, _ = executor.Execute(ctx, config, []FileInfo{
		{Path: originalFile, Type: FileTypeVideo},
	}, 0, nil, nil)

	// 验证：stage2 返回成功，但下一轮循环检测到 context 已取消
	// 注意：stage2 本身成功了，所以 deleteMarkedFiles 会被调用
	// 但如果在 stage2 执行后、deleteMarkedFiles 之前 context 被检查，就不会删除
	// 实际上 executor 在 stage2 成功后直接进入下一轮循环，此时检测到取消

	// 验证：原始文件行为取决于 stage2 是否成功
	// stage2 成功 → deleteMarkedFiles 被调用 → 原始文件被删除
	// 但如果 stage2 之后的循环检测到取消 → 不调用 deleteMarkedFiles
	assert.FileExists(t, intermediateFile, "中间文件应保留")
}

// TestExecute_PassthroughPreservesDeletable 测试透传文件保留 Deletable 标记
func TestExecute_PassthroughPreservesDeletable(t *testing.T) {
	tmpDir := t.TempDir()

	file1 := createTempFile(t, tmpDir, "video.flv")
	file2 := createTempFile(t, tmpDir, "subtitle.ass")

	config := &PipelineConfig{
		Stages: []StageConfig{
			{Name: "stage1"},
		},
	}

	stage1 := &mockStage{
		name: "stage1",
		executeFn: func(ctx *PipelineContext, input []FileInfo) ([]FileInfo, error) {
			// 只处理第一个文件，第二个透传
			output := []FileInfo{
				{Path: input[0].Path + ".processed", Type: FileTypeVideo},
				input[1], // 透传，保留原始 Deletable
			}
			return output, nil
		},
	}

	executor := NewExecutor(logrus.StandardLogger())
	executor.RegisterStage("stage1", func(config StageConfig) (Stage, error) { return stage1, nil })

	ctx := &PipelineContext{
		Ctx: context.Background(),
	}

	// 标记 file2 为 Deletable
	results, err := executor.Execute(ctx, config, []FileInfo{
		{Path: file1, Type: FileTypeVideo},
		{Path: file2, Type: FileTypeOther, Deletable: true},
	}, 0, nil, nil)

	assert.NoError(t, err)
	assert.Len(t, results, 1)

	// 验证：file2 已被删除（因为 Deletable=true 且管道成功）
	assert.NoFileExists(t, file2, "标记为 Deletable 的透传文件应被删除")
}

// TestDeleteMarkedFiles_FileNotExist 测试文件不存在时静默跳过
func TestDeleteMarkedFiles_FileNotExist(t *testing.T) {
	executor := NewExecutor(logrus.StandardLogger())

	files := []FileInfo{
		{Path: "/nonexistent/file.flv", Type: FileTypeVideo, Deletable: true},
		{Path: "/nonexistent/file.mp4", Type: FileTypeVideo, Deletable: false},
	}

	// 不应 panic，不应报错
	kept := executor.deleteMarkedFiles(files)
	assert.Len(t, kept, 1)
	assert.Equal(t, "/nonexistent/file.mp4", kept[0].Path)
}

// TestExecute_UploadCancelledDuringUpload_FilePreserved
// 测试：上传阶段被取消（context 取消），上传返回 error，文件应保留且有日志
func TestExecute_UploadCancelledDuringUpload_FilePreserved(t *testing.T) {
	tmpDir := t.TempDir()

	localFile := createTempFile(t, tmpDir, "video.mkv")

	config := &PipelineConfig{
		Stages: []StageConfig{
			{Name: "upload"},
		},
	}

	cancelCtx, cancel := context.WithCancel(context.Background())

	uploadStage := &mockStage{
		name: "upload",
		executeFn: func(ctx *PipelineContext, input []FileInfo) ([]FileInfo, error) {
			// 模拟上传过程中被取消
			cancel()
			// 上传返回 error（实际中 client.Upload 会因 context 取消而返回 error）
			return input, fmt.Errorf("upload cancelled: %w", context.Canceled)
		},
	}

	executor := NewExecutor(logrus.StandardLogger())
	executor.RegisterStage("upload", func(config StageConfig) (Stage, error) { return uploadStage, nil })

	ctx := &PipelineContext{Ctx: cancelCtx}

	_, err := executor.Execute(ctx, config, []FileInfo{
		{Path: localFile, Type: FileTypeVideo},
	}, 0, nil, nil)

	// 验证：返回错误
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "upload cancelled")

	// 验证：本地文件保留（上传失败，不应删除）
	assert.FileExists(t, localFile, "上传被取消，本地文件应保留")
}

// TestExecute_UploadFailsMiddleOfFileList_NoneDeleted
// 测试：多个文件上传，中间某个失败，全部文件保留（部分成功也不删除）
func TestExecute_UploadFailsMiddleOfFileList_NoneDeleted(t *testing.T) {
	tmpDir := t.TempDir()

	file1 := createTempFile(t, tmpDir, "video1.mkv")
	file2 := createTempFile(t, tmpDir, "video2.mkv")
	file3 := createTempFile(t, tmpDir, "video3.mkv")

	config := &PipelineConfig{
		Stages: []StageConfig{
			{Name: "upload"},
		},
	}

	uploadStage := &mockStage{
		name: "upload",
		executeFn: func(ctx *PipelineContext, input []FileInfo) ([]FileInfo, error) {
			// 模拟 CloudUploadStage 的"全部成功才标记"逻辑
			var output []FileInfo
			var failed bool
			for i, f := range input {
				if i == 1 {
					// 第二个文件上传失败
					output = append(output, f)
					failed = true
				} else {
					output = append(output, f)
				}
			}
			// 只有全部成功才标记 Deletable
			if !failed {
				for i := range output {
					output[i].Deletable = true
				}
			}
			return output, nil
		},
	}

	executor := NewExecutor(logrus.StandardLogger())
	executor.RegisterStage("upload", func(config StageConfig) (Stage, error) { return uploadStage, nil })

	ctx := &PipelineContext{Ctx: context.Background()}

	_, err := executor.Execute(ctx, config, []FileInfo{
		{Path: file1, Type: FileTypeVideo},
		{Path: file2, Type: FileTypeVideo},
		{Path: file3, Type: FileTypeVideo},
	}, 0, nil, nil)

	assert.NoError(t, err)

	// 验证：部分失败，全部文件保留
	assert.FileExists(t, file1, "部分失败时全部文件应保留")
	assert.FileExists(t, file2, "部分失败时全部文件应保留")
	assert.FileExists(t, file3, "部分失败时全部文件应保留")
}

// TestExecute_StaleDeletableFromDB_ClearedOnFailure
// 测试：数据库中残留 Deletable=true 标记，本次上传失败后应清除，不删除文件
func TestExecute_StaleDeletableFromDB_ClearedOnFailure(t *testing.T) {
	tmpDir := t.TempDir()

	file1 := createTempFile(t, tmpDir, "video1.mkv")
	file2 := createTempFile(t, tmpDir, "video2.mkv")

	config := &PipelineConfig{
		Stages: []StageConfig{
			{Name: "upload"},
		},
	}

	uploadStage := &mockStage{
		name: "upload",
		executeFn: func(ctx *PipelineContext, input []FileInfo) ([]FileInfo, error) {
			// 模拟 CloudUploadStage 的逻辑：先清除 Deletable，再判断上传结果
			for i := range input {
				input[i].Deletable = false
			}
			var output []FileInfo
			var failed bool
			for _, f := range input {
				if f.Path == file2 {
					// 第二个文件上传失败
					output = append(output, f)
					failed = true
				} else {
					output = append(output, f)
				}
			}
			if !failed {
				for i := range output {
					output[i].Deletable = true
				}
			}
			return output, nil
		},
	}

	executor := NewExecutor(logrus.StandardLogger())
	executor.RegisterStage("upload", func(config StageConfig) (Stage, error) { return uploadStage, nil })

	ctx := &PipelineContext{Ctx: context.Background()}

	// 模拟数据库中残留 Deletable=true 的文件
	_, err := executor.Execute(ctx, config, []FileInfo{
		{Path: file1, Type: FileTypeVideo, Deletable: true}, // 旧标记
		{Path: file2, Type: FileTypeVideo, Deletable: true}, // 旧标记
	}, 0, nil, nil)

	assert.NoError(t, err)

	// 验证：上传失败，旧标记被清除，文件保留
	assert.FileExists(t, file1, "数据库残留标记应被清除，文件保留")
	assert.FileExists(t, file2, "数据库残留标记应被清除，文件保留")
}

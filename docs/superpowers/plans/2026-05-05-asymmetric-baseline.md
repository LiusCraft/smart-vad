# 非对称基线更新 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 修复大声说话导致噪声基线被人声污染、后续小声说话检测失效的问题

**Architecture:** 在 `computeBaseline()` 中加入非对称平滑：局部估计比当前基线低（更安静）时立即更新，比当前基线高时缓慢跟随（平滑因子 0.05）。`Detect()` 不再重置基线状态，实现跨调用记忆。

**Tech Stack:** Go 1.x, testing package

---

### 文件结构

| 文件 | 职责 | 操作 |
|------|------|------|
| `vad/adaptive.go` | 核心实现 | 修改 |
| `vad/adaptive_test.go` | 单元测试 | 修改 |

---

### Task 1: 写非对称基线的测试用例

**Files:**
- Modify: `vad/adaptive_test.go`

- [ ] **Step 1: 添加三个新测试函数和更新已有配置测试**

在 `vad/adaptive_test.go` 的 `TestComputeBaseline` 后面添加以下测试：

```go
func TestComputeBaselineAsymmetricUp(t *testing.T) {
	a := &AdaptiveDetector{
		inner:      &Detector{minSilenceMs: 500},
		cfg:        AdaptiveConfig{NoiseFloorFrac: 0.1, BaselineSmoothingFactor: 0.05},
		frameDB:    make([]float64, 0, 100),
		capacity:   100,
		baselineDB: -50, // 已有基线，模拟安静环境
	}

	// 全部帧都是 -40dB（模拟被大声说话污染的窗口）
	for i := 0; i < 80; i++ {
		a.addFrame(-40)
	}

	baseline := a.computeBaseline()
	// 局部估计 = -40，但基线应该只缓慢上升
	// new = -50 + 0.05 * (-40 - (-50)) = -50 + 0.5 = -49.5
	if math.Abs(baseline-(-49.5)) > 0.01 {
		t.Errorf("baseline = %.2f, want -49.5 (slow upward smoothing)", baseline)
	}
}

func TestComputeBaselineAsymmetricDown(t *testing.T) {
	a := &AdaptiveDetector{
		inner:      &Detector{minSilenceMs: 500},
		cfg:        AdaptiveConfig{NoiseFloorFrac: 0.1, BaselineSmoothingFactor: 0.05},
		frameDB:    make([]float64, 0, 100),
		capacity:   100,
		baselineDB: -40, // 已有基线，模拟嘈杂环境
	}

	// 全部帧都是 -55dB（环境真的变安静了）
	for i := 0; i < 80; i++ {
		a.addFrame(-55)
	}

	baseline := a.computeBaseline()
	// 局部估计更安静 → 不应该被平滑，应该立即接受
	if math.Abs(baseline-(-55)) > 0.01 {
		t.Errorf("baseline = %.2f, want -55.0 (immediate downward update)", baseline)
	}
}

func TestComputeBaselineFirstCall(t *testing.T) {
	a := &AdaptiveDetector{
		inner:      &Detector{minSilenceMs: 500},
		cfg:        AdaptiveConfig{NoiseFloorFrac: 0.1, BaselineSmoothingFactor: 0.05},
		frameDB:    make([]float64, 0, 100),
		capacity:   100,
		baselineDB: 0, // 首次调用，无历史基线
	}

	for i := 0; i < 80; i++ {
		a.addFrame(-40)
	}

	baseline := a.computeBaseline()
	// 首次调用直接使用局部估计
	if math.Abs(baseline-(-40)) > 0.01 {
		t.Errorf("baseline = %.2f, want -40.0 (first call uses local estimate)", baseline)
	}
}
```

在 `TestAdaptiveConfigValidation` 末尾（`cfg.AdaptMinSpeechMax` 检查之后）添加：

```go
	if cfg.BaselineSmoothingFactor != 0.05 {
		t.Errorf("BaselineSmoothingFactor = %.3f, want 0.05", cfg.BaselineSmoothingFactor)
	}
```

- [ ] **Step 2: 运行测试验证新增测试失败**

```bash
go test ./vad/ -run "TestComputeBaselineAsymmetricUp|TestAdaptiveConfigValidation" -v
```

`TestComputeBaselineAsymmetricUp` 应 FAIL（当前 computeBaseline 返回 -40，期望 -49.5）。
`TestAdaptiveConfigValidation` 应 FAIL（BaselineSmoothingFactor 字段尚未添加/未设默认值）。

- [ ] **Step 3: Commit**

```bash
git add vad/adaptive_test.go
git commit -m "test: add asymmetric baseline smoothing tests"
```

---

### Task 2: 实现非对称基线更新

**Files:**
- Modify: `vad/adaptive.go`

- [ ] **Step 1: 在 `AdaptiveConfig` 结构体中添加 `BaselineSmoothingFactor` 字段**

在 `AdaptiveConfig` 结构体（`vad/adaptive.go:59-70`）的 `DisableRMSPostFilter` 之后添加：

```go
	BaselineSmoothingFactor float64 // 基线上行平滑因子，默认 0.05
```

完整结构体：

```go
type AdaptiveConfig struct {
	DetectorConfig Config

	WindowDuration       float64
	NoiseFloorFrac       float64
	EnergyOffsetDB       float64
	AdaptThresholdMin    float32
	AdaptThresholdMax    float32
	AdaptMinSpeechMin    int
	AdaptMinSpeechMax    int
	DisableRMSPostFilter bool
	BaselineSmoothingFactor float64
}
```

- [ ] **Step 2: 在 `setDefaults()` 中添加默认值**

在 `setDefaults()`（`vad/adaptive.go:72-94`）末尾 `}` 前添加：

```go
	if c.BaselineSmoothingFactor == 0 {
		c.BaselineSmoothingFactor = 0.05
	}
```

- [ ] **Step 3: 修改 `computeBaseline()` 加入非对称平滑**

用以下代码替换 `computeBaseline()`（`vad/adaptive.go:160-180`）：

```go
func (a *AdaptiveDetector) computeBaseline() float64 {
	n := len(a.frameDB)
	if n == 0 {
		if a.baselineDB != 0 {
			return a.baselineDB
		}
		return -60
	}

	sorted := make([]float64, n)
	copy(sorted, a.frameDB)
	sort.Float64s(sorted)

	// Noise floor = average of the quietest NoiseFloorFrac fraction of frames.
	// This captures background noise level, not speech level.
	count := int(math.Ceil(float64(n) * a.cfg.NoiseFloorFrac))
	if count < 1 {
		count = 1
	}
	var sum float64
	for i := 0; i < count; i++ {
		sum += sorted[i]
	}
	localEstimate := sum / float64(count)

	// Asymmetric smoothing: accept quieter baseline immediately,
	// follow louder baseline slowly (speech contamination cannot be trusted).
	if a.baselineDB == 0 {
		a.baselineDB = localEstimate
	} else if localEstimate < a.baselineDB {
		a.baselineDB = localEstimate
	} else {
		a.baselineDB += a.cfg.BaselineSmoothingFactor * (localEstimate - a.baselineDB)
	}

	return a.baselineDB
}
```

- [ ] **Step 4: 从 `Detect()` 中移除 `resetBaseline()` 调用**

在 `Detect()`（`vad/adaptive.go:201-249`）中删除第 202 行的 `a.resetBaseline()`：

```go
func (a *AdaptiveDetector) Detect(pcm []float32) (Result, error) {
	// 删除 a.resetBaseline()

	ws := a.frameSize
	for i := 0; i <= len(pcm)-ws; i += ws {
		a.addFrame(frameRMS(pcm[i : i+ws]))
	}

	baseline := a.computeBaseline()
	...
```

- [ ] **Step 5: 修复 `Process()` 中的基线比较逻辑**

`Process()`（`vad/adaptive.go:271-291`）中 `computeBaseline()` 现在内部更新 `a.baselineDB`，需要在调用前保存旧值。替换函数体：

```go
func (a *AdaptiveDetector) Process(chunk []float32) error {
	ws := a.frameSize
	for i := 0; i <= len(chunk)-ws; i += ws {
		a.addFrame(frameRMS(chunk[i : i+ws]))
	}

	oldBaseline := a.baselineDB
	baseline := a.computeBaseline()
	if math.Abs(baseline-oldBaseline) >= 3 {
		threshold, minSpeechMs, minSilenceMs := a.mapParams(baseline)
		logger.Debug("adaptive params updated",
			"baseline_db", baseline,
			"threshold", threshold,
			"min_speech_ms", minSpeechMs)
		a.inner.SetThreshold(threshold)
		a.inner.SetMinSpeechDurationMs(minSpeechMs)
		a.inner.SetMinSilenceDurationMs(minSilenceMs)
	}

	return a.inner.Process(chunk)
}
```

- [ ] **Step 6: 运行全部测试验证通过**

```bash
go test ./vad/ -v
```

所有测试应 PASS。

- [ ] **Step 7: Commit**

```bash
git add vad/adaptive.go vad/adaptive_test.go
git commit -m "feat: asymmetric baseline smoothing to prevent speech contamination"
```

---

### 任务总结

| 任务 | 描述 | 文件 |
|------|------|------|
| 1 | 写测试 | `vad/adaptive_test.go` |
| 2 | 实现 | `vad/adaptive.go`, `vad/adaptive_test.go` |

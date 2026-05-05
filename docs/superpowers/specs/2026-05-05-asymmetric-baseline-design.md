# 非对称基线更新 — 修复噪声基线被人声污染

## 问题

大声说话时，窗口内所有帧（包括字间间隙）能量都偏高，"最安静的 10%" 帧并非真正的背景噪声，导致基线被高估，后续小声说话无法检测。

## 方案

基线计算加入非对称平滑：

- `localEstimate < 旧基线`（更安静）→ 立即接受为基线
- `localEstimate > 旧基线`（更嘈杂）→ 缓慢平滑上升，因子 0.05

## 改动文件

`vad/adaptive.go`

### 1. `AdaptiveConfig` 新增字段

```go
BaselineSmoothingFactor float64 // 默认 0.05
```

### 2. `computeBaseline()` 加入非对称平滑

```go
func (a *AdaptiveDetector) computeBaseline() float64 {
    n := len(a.frameDB)
    if n == 0 {
        if a.baselineDB != 0 {
            return a.baselineDB
        }
        return -60
    }

    // 局部估计：最安静帧均值
    sorted := make([]float64, n)
    copy(sorted, a.frameDB)
    sort.Float64s(sorted)
    count := max(1, int(math.Ceil(float64(n)*a.cfg.NoiseFloorFrac)))
    var sum float64
    for i := 0; i < count; i++ {
        sum += sorted[i]
    }
    localEstimate := sum / float64(count)

    // 非对称平滑
    if a.baselineDB == 0 {
        a.baselineDB = localEstimate
    } else if localEstimate < a.baselineDB {
        a.baselineDB = localEstimate // 快下
    } else {
        factor := a.cfg.BaselineSmoothingFactor
        a.baselineDB += factor * (localEstimate - a.baselineDB) // 慢上
    }
    return a.baselineDB
}
```

### 3. `Detect()` 删除 `resetBaseline()` 调用

基线生命周期从"每次 Detect 重置"变为"跟随 Detector 生命周期"。`Reset()` 仍保留清空能力。

### 4. `Process()` 适配

`computeBaseline()` 现在内部更新 `a.baselineDB`，Process() 中需要先保存旧值再比较：

```go
oldBaseline := a.baselineDB
baseline := a.computeBaseline()
if math.Abs(baseline-oldBaseline) >= 3 {
    // remap
}
```

## 影响

- 大声说话后基线不会跳变，tiny speech 正常检测
- 真实噪声变化（如开空调）约 2 秒内逐步跟上
- 不影响 RMS 后过滤逻辑

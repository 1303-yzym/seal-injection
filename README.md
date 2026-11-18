# seal-injection

印章注入器：把**白底**的红色印章（公章、私章）图片分离成**透明背景**的印章，再盖到文档里。

仓库名沿用早期的 "Seal Injection"。

## 现在能做什么

- 读入白底（不要求是纯白：允许偏色、阴影、纸张纹理）的红色印章图片，`png`/`jpg` 都行；
- **只留红色印泥**：纸面、黑色/灰色印刷文字、手写笔迹、扫描阴影都变成透明；
- 生成一个 A4 页面的 `.docx`，把印章以浮动图片的方式放到指定位置。

这是最简原型：只“新建一个空文档 + 盖章”，还不会往已有的 `.docx` 里插。

## 用法

```bash
go build -o out/seal-injection.exe .

# 默认：4cm 的章，左上角落在 A4 的 (15cm, 23cm)
out/seal-injection.exe -seal assets/seals/sample-red-seal-moex2013.jpg -out out/seal.docx

# 顺便导出透明背景的印章 PNG，方便先看效果
out/seal-injection.exe -seal 我的印章.jpg -out out/seal.docx -png out/seal.png

# 自定义大小与位置（厘米，A4 页面左上角为原点）
out/seal-injection.exe -seal 我的印章.jpg -out out/seal.docx -size 4.5 -x 12 -y 20

# 强制使用标准公章红（默认是从图里估计）
out/seal-injection.exe -seal 我的印章.jpg -out out/seal.docx -ink "#C8322B"
```

| 参数 | 说明 | 默认值 |
| --- | --- | --- |
| `-seal` | 印章图片路径（必填） | |
| `-out` | 输出 `.docx` 路径 | `out/seal.docx` |
| `-png` | 额外导出透明背景 PNG 的路径 | 不导出 |
| `-size` | 印章宽度（厘米），高度按原图比例 | `4` |
| `-x` / `-y` | 左上角距页面左边 / 上边的距离（厘米） | `15` / `23` |
| `-ink` | 强制印泥颜色，`#RRGGBB` | 自动估计 |
| `-behind` | 把印章放在文字下方 | 在文字上方 |

## 原理

1. 逐像素算“红度” `R - max(G, B)`：纸张、黑字、阴影都近似中性色，红度≈0；
2. 取图像边框区域红度的中位数作为纸张底色并减掉，抵消纸的偏黄/偏蓝；
3. 取红度的 99.5 分位数估计“实心印泥”水平，据此给出软阈值区间，映射成 alpha（边缘平滑、不出锯齿）；
4. 用实心像素的中位数确定印泥颜色，输出统一上色，去掉纸张纹理和色偏；
5. 生成最小 OOXML 包（`document.xml` + 图片），图片用 `wp:anchor` 定位到页面坐标。

## 目录结构

```
main.go                    CLI：读图 → 提取 → 生成 docx
internal/seal/extract.go   红章提取（红度 + 纸底校正 + 软阈值）
internal/docx/docx.go      最简 .docx 生成（浮动图片定位）
assets/seals/              印章图片（示例见 NOTICE.md）
```

## 测试

```bash
go test ./...
```

- `internal/seal`：合成一张“偏黄纸 + 灰色文字 + 红色圆环”的图，验证只保留红章、纸面和灰字被去干净；
- `internal/docx`：验证生成的包结构、XML 合法性、尺寸与偏移是否写进 drawing。

## 示例数据

`assets/seals/` 里的 `sample-red-seal-*.jpg` 是公有领域的真实照片，用作测试样本，
来源与许可见 [assets/seals/NOTICE.md](assets/seals/NOTICE.md)。

## 后续

- [ ] 往**已有** `.docx` 里插章（解包 → 改 `document.xml` → 重新打包）
- [ ] 支持 PDF、图片等其他文件格式
- [ ] 印章在页面上的位置/角度/大小做成更灵活的配置（含骑缝章、多页盖章）
- [ ] 处理蓝色印章、褪色印章、复杂背景（如带底纹的票据）
- [ ] 提取质量调优：去噪点、修复断裂的笔画、保留印章的自然浓淡

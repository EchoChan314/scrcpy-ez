scrcpy-ez 2.4.1 — Windows x64
基于 scrcpy 5.0.1；配套协议 5.0.1-ez2.4.1-release

解压到可写目录，双击 scrcpy-ez.exe。GUI 需要 Microsoft WebView2 运行时。
首次连接请开启设备 USB 调试并授权，也可使用无线调试配对。

本版改善应用列表／图标读取、设备识别兼容、原生规格和自动 DPI。
ABR 根据实际积压介入，区分解码与链路压力，单项锁和双锁继续有效。
高帧率编码工作提示按上限配置；主投屏按设置持有会话亮屏锁，退出后释放。
设置已分类，自动平滑在最底部，可选 1 帧／2 帧缓冲上限，默认关闭。
1 帧偏低延迟，2 帧多一些缓冲余量；等待自动调节，最多 32ms，不补帧。
平滑适用于主投屏／应用投屏及有线／无线，修改后重新投屏生效。
无线默认 60fps，保留设备原生高刷选项；实际帧率由内容和运行条件决定。

更新前退出 EZ 与投屏并备份，保留个人 profiles.json、settings.json、root 偏好和图标缓存。
发行包不含个人设置；不要单独替换 GUI、客户端或 server，应使用整包配套文件。
SHA256SUMS.txt 校验 ZIP，包内 FILE_SHA256SUMS.txt 校验各文件。
SOURCE_MANIFEST.json 记录准确源码提交和运行库哈希，详见 RELEASE_NOTES.md。

GitHub：https://github.com/kinewe/scrcpy-ez/releases/tag/v2.4.1
Gitee：https://gitee.com/kinewe/scrcpy-ez/releases/tag/v2.4.1

# 剪辑DSL v1 · v0.11.0

## 内容与剪辑分开

内容可以是已有或生成的图片、视频、声音、动画成片和文字。DSL只控制剪辑；没有固定新闻段落、角色枚举或“一句一镜”。任意动态图表/动画可先作为图片或视频素材导入，不要求改变DSL。当前并未提供任意组件代码的执行器。

## 最小示例

```json
{
  "version": 1,
  "canvas": {"width":1080,"height":1920,"fps":30},
  "tracks": [{"id":"pictures","kind":"video"},{"id":"titles","kind":"text"}],
  "clips": [
    {"id":"first","track":"pictures","asset":"替换为真实素材ID","start":0,"duration":3},
    {"id":"second","track":"pictures","asset":"另一素材ID","after":"first","duration":2},
    {"id":"headline","track":"titles","start":0,"duration":5,"text":"新闻标题","fontSize":64,"layout":{"x":60,"y":100,"width":960,"height":240},"fadeIn":0.2}
  ]
}
```

MCP先video_asset_list读取真实ID；例子中的占位ID不能直接渲染。画布默认1080×1920、30fps、背景#11182a；零值尺寸/帧率表示默认值。实际画布32–3840偶数、不超过4K像素数，帧率1–60，最长600秒。允许时间间隙，间隙显示背景。

## 轨道与片段

最多20轨、240片段、300锚点。kind为video/audio/text。后面的轨道叠放在前面的轨道之上；同轨重叠按片段声明顺序叠放。音轨混音。视频源音静音，声音须使用单独音频素材。

片段ID稳定且唯一，1–64位字母/数字/下划线/点/横线，以字母开头。每段必须且只能选择start、after、anchor之一，可附offset。after从目标片段结束时间开始。循环引用、缺失ID、负时间拒绝。

source.in/out指定源素材范围，speed为0.5–2（省略或0为默认1）。duration省略时从(in,out,speed)推导；同时指定时必须一致。图片只用duration，不接受源裁剪/变速。实际素材时长在渲染快照阶段通过FFprobe校验，静态编译不等于素材实测。

layout支持x/y/width/height、fit=cover/contain与opacity。文字使用布局宽高和显式换行；溢出裁剪，不自动换行，不接受图片fit。文字必须配置字体；汉字需支持汉字的字体。fontSize为8–240，color为#RRGGBB。

fadeIn/fadeOut为秒，非负且合计不超过片段时长。音频volume为线性0–2，支持源截取、变速、淡入淡出及多轨混音。时间线总长向上对齐到帧边界；音频片段入场按毫秒处理。

锚点例：

```json
{"id":"voice_end","clip":"narration","edge":"end","offset":0.2}
```

也可使用time指定绝对时间。锚点由调用者明确提供，不假称自动语音对齐；第三阶段可将TTS时序转成锚点。

## HTTP与MCP

- GET /api/projects/{id}/edit：revision、active、dsl、编译ir。
- POST /api/projects/{id}/edit/validate：{dsl}，只校验，不保存。
- PUT /api/projects/{id}/edit：{revision,dsl}，保存并启用。
- POST /api/projects/{id}/edit/active：{revision,active}，切换模式但保留两份数据。
- video_edit_get / video_edit_validate / video_edit_update / video_edit_activate。

未知字段拒绝，编译错误返回422及code/target/message。保存检查revision；版本冲突重新读取后合并，不覆盖Agent修改。JSON Schema仅做结构约束，媒体类型、跨引用、时间环、出入点一致性等由编译器补充校验。

渲染使用现有video_job_start(type=render)。激活DSL时使用ffmpeg_edit，忽略旧镜头、旧音轨和旧字幕；反之继续HyperFrames。两种数据不相互投影。字幕可作为文字轨片段表达，不自动导入旧SRT。

## 结果与边界

输出final.mp4、snapshot.json、edit-ir.json、validation.json和日志。MP4校验通过才标记任务成功，快照不随后续编辑变化。

本版支持声明式剪辑，不支持任意关键帧、循环音频、转场名字、动态组件代码、自动换行、逐字对齐或局部渲染。不可用字段会拒绝，不会悄悄忽略。

TTS暂时仍使用旧镜头正文快照；音频通过素材ID加入DSL音轨。原终稿确认只用于旧正文模式，新剪辑终稿绑定后续阶段完成。图形浏览器视觉/播放尚未实测；本版通过实际FFmpeg像素、时序与音频检查。

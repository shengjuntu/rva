# v0.14 描述驱动的文本素材匹配

第四阶段的第一步：让 Codex 检查已有图片或视频首帧，保存画面描述，然后按自由文本取得可解释候选。内容不被规定为“一句话一张图”；匹配不修改 DSL，Codex 可以组合、重复或拒绝素材。

新增 MCP：
- video_asset_describe(projectId, revision, assetId, description={text,tags,basis})。basis 为 user 或 agent_inspection；这里只记录来源声明，不是 App 已检查像素的证明。描述最多8000字节、标签最多30个。仅图片/视频可用。保存增加项目修订。
- video_media_align(projectId, revision, units=[{id,text}], limit=5)。最多100个文本单元、每个8000字节、每单元最多20候选。返回按分数排序的素材 ID、匹配词、描述和来源；无候选明确标记 unmatched。只读并校验修订。

HTTP 对应 PUT /assets/{aid}/description、POST /media/align。素材列表和项目数据包含 description。

算法 description_lexical_dice_v1：英文词项、中文双字片段与描述/标签计算 Dice 词项重合率；相同分数按素材 ID 稳定排序。不使用文件名猜画面，也不会给未描述素材打分。分数不是概率，不是视觉语义模型。视频首帧无法证明整段内容，Agent 应把观察边界写进描述。数字、单字、同义词、否定、事实归属可能无法正确匹配；须由 Codex审查候选，后续可替换为真正的语义适配器。

能力目录现有 text_media_alignment 表示这个明确命名的本地基线；图片生成、视频生成仍未接入，没有假任务。MCP 总数29，安装后更新项目工具。新版基础 Skill 与编辑 Skill 包含检查、描述、匹配、自由编排流程。

验证：Go race 全套；中文词项匹配、稳定排序、无匹配、未描述素材不猜测、版本冲突、描述来源限制、只读匹配；既有 Node 和 DOM/HTTP 流程。真实 Codex素材描述质量、真实语义模型、图片视频生成均未验收。

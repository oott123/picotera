import base64
import httpx2
import os
import time

def encode_image(file_path):
  with open(file_path, "rb") as f:
    base64_image = base64.b64encode(f.read()).decode("utf-8")
  return base64_image

base_url = os.environ["OPENAI_BASE_URL"] or "http://localhost:9898/backend-api/codex"
prompt = """
# Role: 顶尖日系二次元插画师 & 资深世界观视觉概念设计师 (World-building Concept Artist)

# Task:
请根据给定的 {世界观}、{时间线/状态} 以及 {具体地点}，自动检索并提取该 IP/宇宙中最具代表性的视觉符号、底层逻辑与设定（Lore），创作一张具有极强沉浸感、仿佛属于该虚构宇宙内部产物的日系二次元纪实风/概念海报。如存在 {额外要求}，应优先遵守。

# Composition (画面三层架构体系):

- **第一层：原创动漫主角 (Character & Faction Focus)**
  - 主角是一位身处该世界观下的原创动漫少女（如有参考图，请以参考图为原型）。
  - **服装与装备**：必须严格符合该世界观的科技树、魔法体系、生产力水平或特定阵营文化（例如：符合该设定的制服、护甲、常服、特种机能服或民族服饰，并配备相应的标志性随身物品）。
  - **状态与神情**：展现出符合该世界观生存法则与当前环境的情绪（例如：和平日常中的松弛、残酷末日下的警惕、探索未知时的惊奇、高压社会下的麻木等）。

- **第二层：世界观专属生态空间 (Lore-Specific Environment)**
  - 提取该世界观最具辨识度的空间特征，从以下四个普适维度中选择最契合的一项进行深度描绘：
    1. **公共枢纽/聚落**：如繁华的中心主城、跨星系航空港、冒险者公会、异界酒馆、全息商圈、底层贫民窟或魔法学院广场。
    2. **探索/荒野/异常区**：如外星地表、充满魔物的迷宫、规则扭曲的阈限空间、被自然吞噬的废墟遗迹或时空裂缝边缘。
    3. **作业/战斗/机能区**：如机甲整备库、炼金工坊、星舰舰桥、地下实验设施、战壕或重工业流水线。
    4. **私密/庇护空间**：如主角的个人房间、安全屋、帐篷内部、带有该世界观生活痕迹的微型住所。

- **第三层：设定微观细节 (World-building & Easter Eggs)**
  - 将该世界观的“核心彩蛋”和设定碎片自然地散落在环境与背景中：
    1. **核心驱动力与生存道具**：该世界特有的能源形态、通信设备、消耗品或武器（如特定的药剂瓶、数据终端、特殊材质的货币、被污染的补给品）。
    2. **视觉文化、社会符号与文明痕迹**：将该世界的“社会运转逻辑”具象化，不局限于常规的广告牌与旗帜，可以从以下几种层面任选一种或多种：
       * **意识形态与信仰的实体化**：代表该世界核心价值观的视觉锚点（如：纪念特定历史事件的宏大奇观、宗教/神秘学的光影法阵、代表极权的巨型威权建筑、巨企的资本图腾、或是不可名状的神祇祭坛）。
       * **信息交互与大众媒介**：该世界居民获取与传递信息的媒介残留（如：充满奇幻色彩的悬赏令、悬浮的赛博新闻卷轴、浮空的魔法公报、星际航路投影、或精神污染般的洗脑图案）。
       * **阶层、阵营与次文化印记**：体现不同人群、物种或势力碰撞的视觉符号（如：底层抗争者的粗糙涂鸦、特定种族/派系的贵族纹章、地下黑市的暗号记号、不同社会阶层截然不同的建筑涂装与材质）。
       * **历史与岁月的沉积**：展现世界厚度的“过去之物”（如：作为地基的上一代文明残骸、斑驳的神话创世壁画、被改造为民居的旧时代战争机器）。
    3. **独特环境与物理现象**：不属于现实的自然或物理奇观（如：双星系统下的两轮落日、反重力悬浮的碎石、空气中漂浮的数据流光/魔法孢子、异色的雨水与天空）。

# Style & Aesthetic (风格要求):
- **整体风格**：保持新海诚/京都动画级别的高精细度、宏大场景构图与细腻美术设定。
- **视觉层级与留白 (Visual Hierarchy & Negative Space)**：画面必须有“呼吸感”。主角为绝对核心，背景细节可以多，但**绝不可抢戏**。通过适当的环境留白来衬托氛围。
- **世界观滤镜**：根据该IP的基调赋予特定的画面质感（例如：古典奇幻的柔和油画感与丁达尔光、复古科幻的胶片与 CRT 噪点、赛博朋克的高对比度霓虹光污染、恐怖解谜的低照度与压抑冷调）。

# Restriction & Language Rules (限制与文字规范):
1. **严格禁止设定崩坏（Lore-Breaking）**：绝不可出现与该 {世界观} 及 {时间线/状态} 底层逻辑冲突的物品（例如：冷兵器低魔世界不可出现热兵器，硬科幻世界不可出现神秘学魔法阵）。
2. **海报主视觉文案（如标题、宣传语）**：若需排版文案，必须符合该地点的官方/主导语言设定（如该世界观映射英文、日文或多语种混合，请以设定为准；若是完全架空的语系，可用其对应的现实配音语言或抽象符文代替）。
3. **场景内环境文字（如招牌、涂鸦、说明书、商品包装等）**：首要原则是**克制使用**，可以出现，但**只挑选最能体现其世界观特征的文字**。其余的可以考虑使用隐喻性质的“图形符号”（如破损的徽章、特定配色的旗帜、独特的建筑轮廓）来暗示阵营或世界观，而不是直接把过多的文字糊在墙上。需完美契合该世界观的视觉排版风格。文字内容需自然融入背景环境，不要生硬叠加。

# {世界观}: 重返未来1999
# {时间线/状态}: 第二次暴雨前
# {具体地点}: 圣洛夫基金会
# {额外要求}: 遵守参考图的角色设定，服饰自由发挥, large simple color masses, low visual frequency, restrained edge density, clear edge hierarchy, minimal internal contour lines, broad shadow shapes, suppress micro-texture and micro-contrast, no unnecessary specular highlights
"""
prompt_2 = """
生成一张构图和色彩非常干净的电脑壁纸，电脑壁纸的主要角色参考附图。large simple color masses, low visual frequency, restrained edge density, clear edge hierarchy, minimal internal contour lines, broad shadow shapes, suppress micro-texture and micro-contrast, no unnecessary specular highlights
"""

req = httpx2.post(
  f"{base_url}/images/edits",
  headers = {
    "Authorization": f"Bearer {os.environ["OPENAI_API_KEY"]}",
  },
  json = {
    "prompt": prompt_2,
    "background": "auto",
    "size": "auto",
    "quality": "auto",
    "model": "gpt-image-2", # -sunburst
    "images": [
      { "image_url": f"data:image/jpeg;base64,{encode_image('./assets/char_resized.jpg')}" }
    ],
  },
  timeout = 3600.0
)
req.raise_for_status()

result = req.json()
print(result["usage"])

image_base64 = result["data"][0]["b64_json"]
image_bytes = base64.b64decode(image_base64)

with open(f"./assets/{time.time()}.png", "wb") as f:
  f.write(image_bytes)

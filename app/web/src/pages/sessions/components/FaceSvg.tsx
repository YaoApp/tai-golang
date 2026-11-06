import { useEffect, useRef } from 'react';
import type { MotionFrame } from './MotionPanel';

/**
 * 用 SVG 绘制的卡通脸，直接消费 MotionFrame 数据。
 *
 * 设计取舍：原来用写实 3D 人头模型（head.glb）在 200px 小窗里产生明显的
 * 恐怖谷效应——灰色写实五官、眼睛从不眨、缺少唇形变化。换成程序化 SVG
 * 卡通脸后：
 *   - 每个 blendshape 的效果都能直接看到，适合调试 Motion 数据
 *   - 零外部素材，不需要加载 300KB 的 GLB 和 KTX2 转码器
 *   - 形状足够抽象，不会触发恐怖谷
 *
 * 所有更新走 requestAnimationFrame 直接改 SVG 属性，不用 React state，
 * 避免 30~60fps 的重渲染开销。
 */

// ARKit 52 索引（与后端 render-protocol.md §2.6 一致）
const IDX = {
  jawOpen: 0,
  mouthClose: 4,
  mouthFunnel: 5,
  mouthPucker: 6,
  mouthSmileL: 9,
  mouthSmileR: 10,
  mouthFrownL: 11,
  mouthFrownR: 12,
  mouthStretchL: 15,
  mouthStretchR: 16,
  browDownL: 27,
  browDownR: 28,
  browInnerUp: 29,
  browOuterUpL: 30,
  browOuterUpR: 31,
  eyeBlinkL: 40,
  eyeBlinkR: 41,
  eyeSquintL: 42,
  eyeSquintR: 43,
  eyeWideL: 44,
  eyeWideR: 45,
} as const;

interface Props {
  frameRef: React.RefObject<MotionFrame | null>;
}

/**
 * 平滑状态容器：把目标值以帧率无关的指数插值逼近。
 * tau 是时间常数（秒），越大越迟钝。
 */
class Smoother {
  private v: Record<string, number> = {};
  private tau: number;

  constructor(tau: number) {
    this.tau = tau;
  }

  step(key: string, target: number, dt: number, initial = 0): number {
    const cur = this.v[key] ?? initial;
    const alpha = 1 - Math.exp(-dt / this.tau);
    const next = cur + (target - cur) * alpha;
    this.v[key] = next;
    return next;
  }
}

/** 把 0~255 权重归一化到 0~1。 */
function norm(v: number | undefined): number {
  return Math.max(0, Math.min(1, (v ?? 0) / 255));
}

/**
 * 让一个眼内元素（虹膜或高光）跟随瞳孔偏移。
 *
 * @param ref 目标元素
 * @param baseX 该元素未偏移时的横坐标
 * @param dx 相对瞳孔的水平偏移
 * @param dy 相对瞳孔的垂直偏移
 * @param gx 瞳孔当前水平位移
 * @param gy 瞳孔当前垂直位移
 * @param visible 瞳孔是否可见（眨眼时隐藏）
 * @param opacity 可见时的目标不透明度
 */
function setEyeFollow(
  ref: React.RefObject<SVGCircleElement | null>,
  baseX: number,
  dx: number,
  dy: number,
  gx: number,
  gy: number,
  visible: boolean,
  opacity: number,
): void {
  const el = ref.current;
  if (!el) return;
  el.setAttribute('cx', (baseX + gx + dx).toFixed(2));
  el.setAttribute('cy', (92 + gy + dy).toFixed(2));
  el.style.opacity = visible ? String(opacity) : '0';
}

export default function FaceSvg({ frameRef }: Props) {
  // 各个可变元素的 ref，rAF 里直接改属性
  const faceRef = useRef<SVGGElement>(null);
  const mouthRef = useRef<SVGPathElement>(null);
  const mouthFillRef = useRef<SVGPathElement>(null);
  const tongueRef = useRef<SVGEllipseElement>(null);
  const browLRef = useRef<SVGPathElement>(null);
  const browRRef = useRef<SVGPathElement>(null);
  const eyeLRef = useRef<SVGEllipseElement>(null);
  const eyeRRef = useRef<SVGEllipseElement>(null);
  const pupilLRef = useRef<SVGCircleElement>(null);
  const pupilRRef = useRef<SVGCircleElement>(null);
  const irisLRef = useRef<SVGCircleElement>(null);
  const irisRRef = useRef<SVGCircleElement>(null);
  const hl1LRef = useRef<SVGCircleElement>(null);
  const hl1RRef = useRef<SVGCircleElement>(null);
  const hl2LRef = useRef<SVGCircleElement>(null);
  const hl2RRef = useRef<SVGCircleElement>(null);
  const blushLRef = useRef<SVGEllipseElement>(null);
  const blushRRef = useRef<SVGEllipseElement>(null);

  useEffect(() => {
    let raf = 0;
    let last = performance.now();

    // 表情与口型用不同时间常数：口型要跟得上音节，表情要柔和
    const lip = new Smoother(0.04);
    const expr = new Smoother(0.12);
    // 头部与视线
    const head = new Smoother(0.15);

    const tick = (now: number) => {
      const dt = Math.min(0.1, Math.max(0.001, (now - last) / 1000));
      last = now;

      const f = frameRef.current;

      // 无数据时也保持一个静止的基线姿态，不做"断电"跳变
      const face = f?.face ?? [];
      const viseme = f?.viseme ?? [0, 0, 0, 0, 0];
      const hv = f?.head ?? [0, 0, 0];
      const gaze = f?.gaze ?? [0, 0];

      // ---- 头部姿态 ----
      const yaw = head.step('yaw', (hv[0] / 127) * 10, dt);   // ±10deg
      const pitch = head.step('pitch', (hv[1] / 127) * 7, dt); // ±7deg
      const roll = head.step('roll', (hv[2] / 127) * 5, dt);   // ±5deg
      if (faceRef.current) {
        faceRef.current.setAttribute(
          'transform',
          `rotate(${roll.toFixed(2)} 100 100) translate(${(-yaw * 0.35).toFixed(2)} ${(pitch * 0.35).toFixed(2)})`,
        );
      }

      // ---- 眼睛 ----
      const blinkL = norm(face[IDX.eyeBlinkL]);
      const blinkR = norm(face[IDX.eyeBlinkR]);
      const squint = (norm(face[IDX.eyeSquintL]) + norm(face[IDX.eyeSquintR])) / 2;
      const wide = (norm(face[IDX.eyeWideL]) + norm(face[IDX.eyeWideR])) / 2;

      // 眼睛高度：眨眼压扁、眯眼变细、睁大拉高
      const baseH = 10.5;
      const hL = baseH * (1 - blinkL * 0.95) * (1 - squint * 0.35) * (1 + wide * 0.35);
      const hR = baseH * (1 - blinkR * 0.95) * (1 - squint * 0.35) * (1 + wide * 0.35);
      if (eyeLRef.current) {
        eyeLRef.current.setAttribute('ry', Math.max(0.6, hL).toFixed(2));
      }
      if (eyeRRef.current) {
        eyeRRef.current.setAttribute('ry', Math.max(0.6, hR).toFixed(2));
      }

      // 瞳孔跟随 gaze，并在眨眼时隐藏（避免闭眼后瞳孔悬在外面）
      const gx = (gaze[0] / 127) * 3.2;
      const gy = (gaze[1] / 127) * 2.2;
      const pupilVisible = Math.max(blinkL, blinkR) < 0.6;
      for (const ref of [pupilLRef, pupilRRef, irisLRef, irisRRef]) {
        if (!ref.current) continue;
        ref.current.style.opacity = pupilVisible ? '1' : '0';
      }
      if (pupilLRef.current) {
        pupilLRef.current.setAttribute('cx', (76 + gx).toFixed(2));
        pupilLRef.current.setAttribute('cy', (92 + gy).toFixed(2));
      }
      if (pupilRRef.current) {
        pupilRRef.current.setAttribute('cx', (124 + gx).toFixed(2));
        pupilRRef.current.setAttribute('cy', (92 + gy).toFixed(2));
      }
      // 虹膜与高光跟随瞳孔：高光相对瞳孔的偏移固定，保持「光源在上方」的观感
      setEyeFollow(irisLRef, 76, 0, 0, gx, gy, pupilVisible, 1);
      setEyeFollow(irisRRef, 124, 0, 0, gx, gy, pupilVisible, 1);
      setEyeFollow(hl1LRef, 76, 2.6, -2.6, gx, gy, pupilVisible, 0.95);
      setEyeFollow(hl1RRef, 124, 2.6, -2.6, gx, gy, pupilVisible, 0.95);
      setEyeFollow(hl2LRef, 76, -2, 2.6, gx, gy, pupilVisible, 0.55);
      setEyeFollow(hl2RRef, 124, -2, 2.6, gx, gy, pupilVisible, 0.55);

      // ---- 眉毛 ----
      // 眉毛的竖直偏移由 browDown（下压）与 browInnerUp/browOuterUp（上提）叠加
      const browInner = norm(face[IDX.browInnerUp]);
      const browDownL = norm(face[IDX.browDownL]);
      const browDownR = norm(face[IDX.browDownR]);
      const browUpL = norm(face[IDX.browOuterUpL]);
      const browUpR = norm(face[IDX.browOuterUpR]);

      // 内侧（靠近鼻梁）与外侧（靠近太阳穴）分别计算
      const innerLift = browInner * 6;
      const outerLiftL = browUpL * 7 - browDownL * 6;
      const outerLiftR = browUpR * 7 - browDownR * 6;

      if (browLRef.current) {
        const y = expr.step('browLy', outerLiftL, dt);
        // 左眉：内端在 x=74，外端在 x=52
        browLRef.current.setAttribute(
          'd',
          `M 52 ${72 - y} Q 63 ${66 - y - innerLift * 0.4} 74 ${70 - innerLift}`,
        );
      }
      if (browRRef.current) {
        const y = expr.step('browRy', outerLiftR, dt);
        browRRef.current.setAttribute(
          'd',
          `M 126 ${72 - y} Q 137 ${66 - y - innerLift * 0.4} 148 ${70 - innerLift}`,
        );
      }

      // ---- 嘴 ----
      // 张口高度：jawOpen 为主
      const jaw = lip.step('jaw', norm(face[IDX.jawOpen]), dt);
      // 唇形：aa 张大、ou 收圆、ih 展宽
      const vAA = norm(viseme[0]);
      const vIH = norm(viseme[1]);
      const vEE = norm(viseme[2]);
      const vOH = norm(viseme[3]);
      const vOU = norm(viseme[4]);
      // ARKit 侧的圆展（后端已算好，作为补充信号）
      const funnel = lip.step('funnel', norm(face[IDX.mouthFunnel]), dt);
      const pucker = lip.step('pucker', norm(face[IDX.mouthPucker]), dt);
      const stretch = lip.step('stretch', norm(face[IDX.mouthStretchL]), dt);
      const close = lip.step('close', norm(face[IDX.mouthClose]), dt);

      // 嘴角上扬/下垂
      const smile = expr.step(
        'smile',
        (norm(face[IDX.mouthSmileL]) + norm(face[IDX.mouthSmileR])) / 2,
        dt,
      );
      const frown = expr.step(
        'frown',
        (norm(face[IDX.mouthFrownL]) + norm(face[IDX.mouthFrownR])) / 2,
        dt,
      );

      // 嘴宽：基础 34，ih/ee 展宽，ou/oh 收窄
      const wideAmt = Math.max(vIH, vEE);
      const roundAmt = Math.max(vOU, vOH) * 0.8 + funnel * 0.5 + pucker * 0.4;
      const width = 34 + wideAmt * 12 + stretch * 8 - roundAmt * 12 + smile * 4;
      // 嘴高：jaw 主导，配合元音开合
      const openAmt = Math.max(0, jaw * 30 + vAA * 6 - close * 14);
      // 圆度：ou 让嘴更接近椭圆（宽高比接近 1）
      const roundness = Math.min(1, roundAmt);

      const halfW = width / 2;
      const halfH = Math.max(0.8, openAmt / 2);
      const cx = 100;
      const cy = 132;

      // 用椭圆弧构造嘴形；圆唇时控制点更靠内
      const ctrl = halfW * (1 - roundness * 0.35);
      const topY = cy - halfH;
      const botY = cy + halfH;
      // 嘴角上扬/下垂
      const cornerLift = (smile - frown) * 5;
      const leftCornerY = cy + (halfH < 1.5 ? 0 : 0) - cornerLift;
      const rightCornerY = cy - cornerLift;

      const d =
        `M ${cx - halfW} ${leftCornerY} ` +
        `Q ${cx - ctrl} ${topY} ${cx} ${topY} ` +
        `Q ${cx + ctrl} ${topY} ${cx + halfW} ${rightCornerY} ` +
        `Q ${cx + ctrl} ${botY} ${cx} ${botY} ` +
        `Q ${cx - ctrl} ${botY} ${cx - halfW} ${leftCornerY} Z`;

      if (mouthRef.current) mouthRef.current.setAttribute('d', d);
      if (mouthFillRef.current) {
        mouthFillRef.current.setAttribute('d', d);
        // 张得足够大时才露出深色口腔
        mouthFillRef.current.style.opacity = openAmt > 5 ? '1' : '0';
      }
      if (tongueRef.current) {
        const showTongue = openAmt > 12;
        tongueRef.current.style.opacity = showTongue ? '0.9' : '0';
        tongueRef.current.setAttribute('cy', (botY - Math.min(4, halfH * 0.35)).toFixed(2));
      }

      // ---- 腮红：只在强正面情绪时出现 ----
      const blush = Math.min(1, smile * 1.2);
      for (const ref of [blushLRef, blushRRef]) {
        if (!ref.current) continue;
        ref.current.style.opacity = (blush * 0.55).toFixed(2);
      }

      raf = requestAnimationFrame(tick);
    };

    raf = requestAnimationFrame(tick);
    return () => cancelAnimationFrame(raf);
  }, [frameRef]);

  return (
    <svg
      viewBox="0 0 200 200"
      style={{ width: '100%', height: '100%', display: 'block' }}
      role="img"
      aria-label="Avatar preview"
    >
      <defs>
        <radialGradient id="faceGrad" cx="50%" cy="42%" r="62%">
          <stop offset="0%" stopColor="#ffe9d6" />
          <stop offset="100%" stopColor="#f2c9a8" />
        </radialGradient>
        <linearGradient id="hairGrad" x1="0" y1="0" x2="0" y2="1">
          <stop offset="0%" stopColor="#4a5568" />
          <stop offset="100%" stopColor="#2d3748" />
        </linearGradient>
      </defs>

      {/* 背景：柔和的圆形底色，避免白底刺眼 */}
      <rect width="200" height="200" fill="#eef3fb" />
      <circle cx="100" cy="100" r="86" fill="#dce8f7" />

      <g ref={faceRef} style={{ transition: 'transform 0.05s linear' }}>
        {/* 头发（后层） */}
        <ellipse cx="100" cy="88" rx="58" ry="60" fill="url(#hairGrad)" />

        {/* 脸 */}
        <ellipse cx="100" cy="100" rx="48" ry="52" fill="url(#faceGrad)" stroke="#e0b391" strokeWidth="0.8" />

        {/* 耳朵 */}
        <ellipse cx="52" cy="104" rx="6" ry="9" fill="#f7d9bf" stroke="#e0b391" strokeWidth="0.6" />
        <ellipse cx="148" cy="104" rx="6" ry="9" fill="#f7d9bf" stroke="#e0b391" strokeWidth="0.6" />

        {/* 刘海 */}
        <path
          d="M 54 72 Q 60 44 100 42 Q 140 44 146 72 Q 138 60 118 58 Q 104 70 88 58 Q 66 60 54 72 Z"
          fill="url(#hairGrad)"
        />

        {/* 眉毛 */}
        <path ref={browLRef} d="M 52 72 Q 63 66 74 70" stroke="#3f4a5a" strokeWidth="2.6" fill="none" strokeLinecap="round" />
        <path ref={browRRef} d="M 126 72 Q 137 66 148 70" stroke="#3f4a5a" strokeWidth="2.6" fill="none" strokeLinecap="round" />

        {/* 眼睛（白眼球）：细长形，瞳孔占比大才显得有神 */}
        <ellipse ref={eyeLRef} cx="76" cy="92" rx="8.5" ry="10.5" fill="#fdfdfd" stroke="#bfa08a" strokeWidth="0.6" />
        <ellipse ref={eyeRRef} cx="124" cy="92" rx="8.5" ry="10.5" fill="#fdfdfd" stroke="#bfa08a" strokeWidth="0.6" />

        {/* 瞳孔：大瞳孔更亲和，小瞳孔显得惊悚 */}
        <circle ref={pupilLRef} cx="76" cy="92" r="6.4" fill="#31404f" />
        <circle ref={pupilRRef} cx="124" cy="92" r="6.4" fill="#31404f" />
        {/* 虹膜内圈，增加层次 */}
        <circle ref={irisLRef} cx="76" cy="92" r="4.2" fill="#4a5d70" opacity="0.75" />
        <circle ref={irisRRef} cx="124" cy="92" r="4.2" fill="#4a5d70" opacity="0.75" />
        {/* 高光 */}
        <circle ref={hl1LRef} cx="78.6" cy="89.4" r="2.1" fill="#ffffff" opacity="0.95" />
        <circle ref={hl1RRef} cx="126.6" cy="89.4" r="2.1" fill="#ffffff" opacity="0.95" />
        <circle ref={hl2LRef} cx="74" cy="94.6" r="0.9" fill="#ffffff" opacity="0.55" />
        <circle ref={hl2RRef} cx="122" cy="94.6" r="0.9" fill="#ffffff" opacity="0.55" />

        {/* 腮红 */}
        <ellipse ref={blushLRef} cx="68" cy="112" rx="9" ry="5" fill="#ff9f9f" opacity="0" />
        <ellipse ref={blushRRef} cx="132" cy="112" rx="9" ry="5" fill="#ff9f9f" opacity="0" />

        {/* 鼻子 */}
        <path d="M 100 100 Q 97 112 100 114" stroke="#dfae8d" strokeWidth="1.6" fill="none" strokeLinecap="round" />

        {/* 嘴：外形描边 + 内腔填充 + 舌头 */}
        <path ref={mouthRef} d="M 83 132 Q 91 128 100 128 Q 109 128 117 132 Q 109 136 100 136 Q 91 136 83 132 Z" fill="#c96b6b" opacity="0.85" />
        <path ref={mouthFillRef} d="" fill="#5c2733" style={{ opacity: 0 }} />
        <ellipse ref={tongueRef} cx="100" cy="134" rx="6" ry="3" fill="#e07a7a" style={{ opacity: 0 }} />
      </g>
    </svg>
  );
}

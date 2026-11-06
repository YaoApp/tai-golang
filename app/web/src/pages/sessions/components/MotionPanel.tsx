import { useRef, useState, useCallback, useEffect } from 'react';
import FaceSvg from './FaceSvg';
import Button from '../../../components/ui/Button';

// MotionFrame mirrors the Go MotionFrame JSON.
export interface MotionFrame {
  version: number;
  profile: number;
  state: number;    // 0=idle,1=listening,2=thinking,3=speaking
  emotion: number;  // 0=neutral,1=happy,2=surprised,3=confused,4=concerned
  intensity: number;
  frame_seq: number;
  audio_seq: number;
  pts_ms: number;
  face: number[];   // 52 ARKit blendshape weights 0-255
  head: number[];   // [yaw, pitch, roll] int8
  gaze: number[];   // [x, y] int8
  viseme: number[]; // [aa, ih, ee, oh, ou] 0-255
  gesture_id: number;
  gesture_phase: number;
  body: number[];   // 8 values
}

// ARKit 52 blendshape names in index order (render-protocol.md §2.2)
const ARKIT_NAMES = [
  'jawOpen','jawForward','jawLeft','jawRight',
  'mouthClose','mouthFunnel','mouthPucker','mouthLeft','mouthRight',
  'mouthSmileLeft','mouthSmileRight','mouthFrownLeft','mouthFrownRight',
  'mouthDimpleLeft','mouthDimpleRight','mouthStretchLeft','mouthStretchRight',
  'mouthRollLower','mouthRollUpper','mouthShrugLower','mouthShrugUpper',
  'mouthPressLeft','mouthPressRight','mouthLowerDownLeft','mouthLowerDownRight',
  'mouthUpperUpLeft','mouthUpperUpRight',
  'browDownLeft','browDownRight','browInnerUp','browOuterUpLeft','browOuterUpRight',
  'eyeLookUpLeft','eyeLookUpRight','eyeLookDownLeft','eyeLookDownRight',
  'eyeLookInLeft','eyeLookInRight','eyeLookOutLeft','eyeLookOutRight',
  'eyeBlinkLeft','eyeBlinkRight','eyeSquintLeft','eyeSquintRight',
  'eyeWideLeft','eyeWideRight',
  'cheekPuff','cheekSquintLeft','cheekSquintRight',
  'noseSneerLeft','noseSneerRight','tongueOut',
];

const STATE_LABELS = ['Idle', 'Listening', 'Thinking', 'Speaking'];
const EMOTION_LABELS = ['Neutral', 'Happy', 'Surprised', 'Confused', 'Concerned'];
const STATE_COLORS = ['#999', '#2196f3', '#ff9800', '#4caf50'];
const EMOTION_EMOJIS = ['😐', '😊', '😮', '🤔', '😟'];

interface Props {
  frameRef: React.RefObject<MotionFrame | null>;
  visible: boolean;
  onClose: () => void;
}

// Key blendshape indices for the summary bar view
const KEY_INDICES = [0, 9, 10, 27, 28, 29, 40, 41, 44, 45, 46, 51];

export default function MotionPanel({ frameRef, visible, onClose }: Props) {
  const [pos, setPos] = useState({ x: window.innerWidth - 340, y: window.innerHeight - 460 });
  const [minimized, setMinimized] = useState(false);
  const [expanded, setExpanded] = useState(false);
  const dragging = useRef(false);
  const dragOffset = useRef({ x: 0, y: 0 });

  // FPS counter（lastTime 延迟到首次 tick 初始化，避免在渲染期调用 Date.now）
  const fpsRef = useRef<{ count: number; lastTime: number; fps: number } | null>(null);
  const fpsLabelRef = useRef<HTMLSpanElement>(null);

  // Bar refs for direct DOM updates (no re-render)
  const barRefs = useRef<(HTMLDivElement | null)[]>([]);
  const valRefs = useRef<(HTMLSpanElement | null)[]>([]);
  const stateRef = useRef<HTMLSpanElement>(null);
  const emotionRef = useRef<HTMLSpanElement>(null);
  const intensityBarRef = useRef<HTMLDivElement>(null);
  const headRef = useRef<HTMLSpanElement>(null);
  const visemeRef = useRef<HTMLSpanElement>(null);

  // rAF loop to update DOM directly
  useEffect(() => {
    if (!visible || minimized) return;
    let raf: number;
    const indices = expanded ? Array.from({ length: 52 }, (_, i) => i) : KEY_INDICES;

    const tick = () => {
      const f = frameRef.current;
      if (f) {
        // FPS：首次进入时惰性初始化计时起点
        const now = Date.now();
        if (!fpsRef.current) {
          fpsRef.current = { count: 0, lastTime: now, fps: 0 };
        }
        fpsRef.current.count++;
        if (now - fpsRef.current.lastTime >= 1000) {
          fpsRef.current.fps = fpsRef.current.count;
          fpsRef.current.count = 0;
          fpsRef.current.lastTime = now;
          if (fpsLabelRef.current) fpsLabelRef.current.textContent = `${fpsRef.current.fps} fps`;
        }

        // Bars
        for (let j = 0; j < indices.length; j++) {
          const i = indices[j];
          const v = f.face[i] ?? 0;
          const pct = (v / 255) * 100;
          if (barRefs.current[j]) barRefs.current[j]!.style.width = `${pct}%`;
          if (valRefs.current[j]) valRefs.current[j]!.textContent = `${v}`;
        }
        // State / Emotion
        if (stateRef.current) {
          stateRef.current.textContent = `${STATE_LABELS[f.state] || '?'}`;
          stateRef.current.style.color = STATE_COLORS[f.state] || '#999';
        }
        if (emotionRef.current) {
          emotionRef.current.textContent = `${EMOTION_EMOJIS[f.emotion] || ''} ${EMOTION_LABELS[f.emotion] || '?'} `;
        }
        if (intensityBarRef.current) {
          intensityBarRef.current.style.width = `${(f.intensity / 255) * 100}%`;
        }
        if (headRef.current) {
          headRef.current.textContent = `Y${f.head[0]} P${f.head[1]} R${f.head[2]}`;
        }
        if (visemeRef.current) {
          const labels = ['aa', 'ih', 'ee', 'oh', 'ou'];
          visemeRef.current.textContent = labels.map((l, i) => `${l}:${f.viseme[i]}`).join(' ');
        }
      }
      raf = requestAnimationFrame(tick);
    };
    raf = requestAnimationFrame(tick);
    return () => cancelAnimationFrame(raf);
  }, [visible, minimized, expanded, frameRef]);

  // Drag handlers
  const onMouseDown = useCallback((e: React.MouseEvent) => {
    dragging.current = true;
    dragOffset.current = { x: e.clientX - pos.x, y: e.clientY - pos.y };
    e.preventDefault();
  }, [pos]);

  useEffect(() => {
    const onMove = (e: MouseEvent) => {
      if (!dragging.current) return;
      setPos({ x: e.clientX - dragOffset.current.x, y: e.clientY - dragOffset.current.y });
    };
    const onUp = () => { dragging.current = false; };
    window.addEventListener('mousemove', onMove);
    window.addEventListener('mouseup', onUp);
    return () => {
      window.removeEventListener('mousemove', onMove);
      window.removeEventListener('mouseup', onUp);
    };
  }, []);

  const indices = expanded ? Array.from({ length: 52 }, (_, i) => i) : KEY_INDICES;

  if (!visible) return null;

  if (minimized) {
    return (
      <div
        className="motion motion--mini"
        style={{ left: pos.x, top: pos.y }}
        onClick={() => setMinimized(false)}
      >
        <span className="motion__mini-label">
          Motion <span className="chat__dot chat__dot--on" />
        </span>
      </div>
    );
  }

  return (
    <div className="motion" style={{ left: pos.x, top: pos.y }}>
      {/* Title bar — draggable */}
      <div className="motion__title" onMouseDown={onMouseDown}>
        <span className="motion__title-text">
          Motion
          <span ref={fpsLabelRef} className="motion__fps mono">0 fps</span>
        </span>
        <span className="row" style={{ gap: 2 }}>
          <Button size="sm" variant="ghost" onClick={() => setMinimized(true)}>_</Button>
          <Button size="sm" variant="ghost" onClick={onClose}>×</Button>
        </span>
      </div>

      {/* Procedural SVG face. The face palette is the avatar's own rendering
          semantics, not console chrome, so it keeps its colors. */}
      <div className="motion__face">
        <FaceSvg frameRef={frameRef} />
      </div>

      {/* Status row */}
      <div className="motion__status">
        <span>State: <span ref={stateRef} className="motion__value">Idle</span></span>
        <span ref={emotionRef}>Neutral</span>
        <span ref={headRef} className="motion__hud mono" style={{ marginLeft: 'auto' }}>Y0 P0 R0</span>
      </div>

      {/* Intensity bar */}
      <div className="motion__meter">
        <span className="motion__meter-label">Intensity</span>
        <div className="motion__track">
          <div ref={intensityBarRef} className="motion__fill motion__fill--warning" style={{ width: '0%' }} />
        </div>
      </div>

      {/* Viseme row */}
      <div className="motion__hud mono">
        Viseme: <span ref={visemeRef}>aa:0 ih:0 ee:0 oh:0 ou:0</span>
      </div>

      {/* Blendshape bars */}
      <div className="motion__bars" style={{ maxHeight: expanded ? 320 : 160 }}>
        {indices.map((idx, j) => (
          <div key={idx} className="motion__bar-row">
            <span className="motion__bar-name mono" style={{ width: expanded ? 130 : 80 }}>
              {ARKIT_NAMES[idx]}
            </span>
            <div className="motion__track">
              <div
                ref={(el) => { barRefs.current[j] = el; }}
                className="motion__fill"
                style={{ background: barColor(idx), width: '0%' }}
              />
            </div>
            <span
              ref={(el) => { valRefs.current[j] = el; }}
              className="motion__bar-value mono"
            >
              0
            </span>
          </div>
        ))}
      </div>

      <Button size="sm" variant="ghost" onClick={() => setExpanded(!expanded)} style={{ width: '100%' }}>
        {expanded ? 'Collapse' : 'All 52 blendshapes'}
      </Button>
    </div>
  );
}

function barColor(idx: number): string {
  if (idx <= 26 || idx === 51) return '#4d9eff'; // mouth/jaw
  if (idx <= 31) return '#d29922'; // brow
  if (idx <= 45) return '#3fb950'; // eye
  return '#bc8cff'; // cheek/nose
}

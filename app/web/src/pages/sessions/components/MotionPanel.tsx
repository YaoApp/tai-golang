import { useRef, useState, useCallback, useEffect, useMemo, Suspense } from 'react';
import { Canvas, useFrame, useThree, useLoader } from '@react-three/fiber';
import { GLTFLoader } from 'three/examples/jsm/loaders/GLTFLoader.js';
import { KTX2Loader } from 'three/examples/jsm/loaders/KTX2Loader.js';
import { MeshoptDecoder } from 'three/examples/jsm/libs/meshopt_decoder.module.js';
import * as THREE from 'three';

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

  // FPS counter
  const fpsRef = useRef({ count: 0, lastTime: Date.now(), fps: 0 });
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
        // FPS
        fpsRef.current.count++;
        const now = Date.now();
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
        style={{ ...floatBase, left: pos.x, top: pos.y, width: 'auto', cursor: 'pointer', padding: '4px 12px' }}
        onClick={() => setMinimized(false)}
      >
        <span style={{ fontSize: 12, fontWeight: 600, color: '#555' }}>
          Motion <span ref={stateRef} style={{ color: '#4caf50' }}>●</span>
        </span>
      </div>
    );
  }

  return (
    <div style={{ ...floatBase, left: pos.x, top: pos.y, width: 320 }}>
      {/* Title bar — draggable */}
      <div style={titleBar} onMouseDown={onMouseDown}>
        <span style={{ fontSize: 12, fontWeight: 600, color: '#333' }}>
          ≡ Motion <span ref={fpsLabelRef} style={{ color: '#999', fontWeight: 400, fontSize: 10 }}>0 fps</span>
        </span>
        <span>
          <button onClick={() => setMinimized(true)} style={ctrlBtn}>_</button>
          <button onClick={onClose} style={ctrlBtn}>×</button>
        </span>
      </div>

      {/* Three.js 3D head */}
      <div style={{ height: 200, background: 'linear-gradient(180deg, #e8f0fe 0%, #f5e6d3 100%)', borderRadius: 6, overflow: 'hidden' }}>
        <Canvas camera={{ position: [0, 0, 1.8], fov: 40 }}>
          <ambientLight intensity={0.8} />
          <directionalLight position={[3, 4, 5]} intensity={0.6} />
          <directionalLight position={[-2, 1, 3]} intensity={0.3} color="#ffe4cc" />
          <Suspense fallback={null}>
            <FacecapHead frameRef={frameRef} />
          </Suspense>
        </Canvas>
      </div>

      {/* Status row */}
      <div style={{ display: 'flex', gap: 12, padding: '6px 0', fontSize: 11, alignItems: 'center' }}>
        <span>State: <span ref={stateRef} style={{ fontWeight: 600 }}>Idle</span></span>
        <span ref={emotionRef}>😐 Neutral</span>
        <span ref={headRef} style={{ color: '#888', fontSize: 10, fontFamily: 'monospace' }}>Y0 P0 R0</span>
      </div>

      {/* Intensity bar */}
      <div style={{ display: 'flex', alignItems: 'center', gap: 6, fontSize: 10, color: '#666', marginBottom: 4 }}>
        <span>Intensity</span>
        <div style={{ flex: 1, height: 4, background: '#eee', borderRadius: 2, overflow: 'hidden' }}>
          <div ref={intensityBarRef} style={{ height: '100%', background: '#ff9800', width: '0%', transition: 'width 0.05s' }} />
        </div>
      </div>

      {/* Viseme row */}
      <div style={{ fontSize: 10, color: '#888', fontFamily: 'monospace', marginBottom: 6 }}>
        Viseme: <span ref={visemeRef}>aa:0 ih:0 ee:0 oh:0 ou:0</span>
      </div>

      {/* Blendshape bars */}
      <div style={{ maxHeight: expanded ? 320 : 160, overflowY: 'auto', fontSize: 10 }}>
        {indices.map((idx, j) => (
          <div key={idx} style={{ display: 'flex', alignItems: 'center', gap: 4, marginBottom: 2 }}>
            <span style={{ width: expanded ? 130 : 80, textAlign: 'right', color: '#666', overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap', fontFamily: 'monospace', fontSize: 9 }}>
              {ARKIT_NAMES[idx]}
            </span>
            <div style={{ flex: 1, height: 6, background: '#f0f0f0', borderRadius: 3, overflow: 'hidden' }}>
              <div ref={(el) => { barRefs.current[j] = el; }} style={{ height: '100%', background: barColor(idx), width: '0%' }} />
            </div>
            <span ref={(el) => { valRefs.current[j] = el; }} style={{ width: 24, textAlign: 'right', color: '#999', fontFamily: 'monospace' }}>0</span>
          </div>
        ))}
      </div>

      <button onClick={() => setExpanded(!expanded)} style={{ ...ctrlBtn, width: '100%', marginTop: 4, fontSize: 10 }}>
        {expanded ? '▲ Collapse' : `▼ All 52 blendshapes`}
      </button>
    </div>
  );
}

function barColor(idx: number): string {
  if (idx <= 26 || idx === 51) return '#2196f3'; // mouth/jaw
  if (idx <= 31) return '#ff9800'; // brow
  if (idx <= 45) return '#4caf50'; // eye
  return '#9c27b0'; // cheek/nose
}

// ---------- GLB Face Model (three.js facecap) ----------
// Maps MotionFrame face[0..51] to the model's morph target indices.
// face[] uses render-protocol.md order; GLB uses Apple's original order with _L/_R suffixes.
const FACE_TO_MORPH = [24,25,26,27,36,28,29,30,31,37,38,39,40,41,42,49,50,33,32,35,34,47,48,45,46,43,44,1,2,0,3,4,5,6,7,8,9,10,11,12,13,14,15,16,17,18,19,20,21,22,23,51];

function FacecapHead({ frameRef }: { frameRef: React.RefObject<MotionFrame | null> }) {
  const { gl } = useThree();
  const gltf = useLoader(GLTFLoader, '/models/head.glb', (loader) => {
    const ktx2 = new KTX2Loader();
    ktx2.setTranscoderPath('https://cdn.jsdelivr.net/gh/mrdoob/three.js@r175/examples/jsm/libs/basis/');
    ktx2.detectSupport(gl);
    loader.setKTX2Loader(ktx2);
    loader.setMeshoptDecoder(MeshoptDecoder);
  });
  const groupRef = useRef<THREE.Group>(null);

  // Clone scene and compute centering transform once
  const { clonedScene, morphMeshes, offset, scale } = useMemo(() => {
    const cloned = gltf.scene.clone(true);
    const meshes: THREE.Mesh[] = [];
    cloned.traverse((node) => {
      if ((node as THREE.Mesh).isMesh) {
        if ((node as THREE.Mesh).morphTargetInfluences) {
          meshes.push(node as THREE.Mesh);
        }
      }
    });
    // Auto-center: compute bounding box, derive offset and scale
    const box = new THREE.Box3().setFromObject(cloned);
    const center = box.getCenter(new THREE.Vector3());
    const size = box.getSize(new THREE.Vector3());
    const maxDim = Math.max(size.x, size.y, size.z);
    // Target: fill ~1.2 units so it looks good at fov 40, camera z=1.8
    const s = maxDim > 0 ? 1.2 / maxDim : 1;
    return { clonedScene: cloned, morphMeshes: meshes, offset: center, scale: s };
  }, [gltf.scene]);

  useFrame(() => {
    const f = frameRef.current;
    if (!groupRef.current) return;

    // Head rotation (apply even without frame data for idle pose)
    if (f) {
      groupRef.current.rotation.set(
        (f.head[1] / 127) * 0.4,
        (f.head[0] / 127) * 0.5,
        (f.head[2] / 127) * 0.25,
      );

      // Apply all 52 blendshapes
      for (const mesh of morphMeshes) {
        const influences = mesh.morphTargetInfluences!;
        for (let i = 0; i < 52; i++) {
          const morphIdx = FACE_TO_MORPH[i];
          if (morphIdx >= 0 && morphIdx < influences.length) {
            influences[morphIdx] = f.face[i] / 255;
          }
        }
      }
    }
  });

  return (
    <group ref={groupRef} scale={scale}>
      <group position={[-offset.x, -offset.y, -offset.z]}>
        <primitive object={clonedScene} />
      </group>
    </group>
  );
}

// ---------- Styles ----------

const floatBase: React.CSSProperties = {
  position: 'fixed',
  background: '#fff',
  border: '1px solid #ddd',
  borderRadius: 10,
  boxShadow: '0 8px 32px rgba(0,0,0,0.18)',
  padding: 10,
  zIndex: 9999,
  userSelect: 'none',
};

const titleBar: React.CSSProperties = {
  display: 'flex',
  justifyContent: 'space-between',
  alignItems: 'center',
  cursor: 'grab',
  padding: '2px 0 6px',
  borderBottom: '1px solid #eee',
  marginBottom: 6,
};

const ctrlBtn: React.CSSProperties = {
  background: 'none',
  border: '1px solid #ddd',
  borderRadius: 4,
  cursor: 'pointer',
  fontSize: 12,
  padding: '0 6px',
  marginLeft: 4,
  color: '#666',
  lineHeight: '20px',
};

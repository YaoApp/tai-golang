import { useEffect, useRef, useMemo } from 'react';
import type { WSMessage } from '../../../hooks/useWebSocket';

interface Props {
  renderMessages: WSMessage[];
  realtimeActive: boolean;
  micAnalyser: AnalyserNode | null;
  playAnalyser: AnalyserNode | null;
  onClose: () => void;
}

interface SubtitleEntry {
  id: string;
  role: 'user' | 'assistant';
  chunks: string[];
  complete: boolean;
  durationMs?: number;
  emotion?: { state: string; emotion: string; intensity: number };
}

export default function SubtitlePanel({ renderMessages, realtimeActive, micAnalyser, playAnalyser, onClose }: Props) {
  const endRef = useRef<HTMLDivElement>(null);

  const entries = useMemo(() => {
    const result: SubtitleEntry[] = [];
    let currentAssistant: SubtitleEntry | null = null;

    for (const m of renderMessages) {
      if (m.direction === 'out') continue;
      const d = m.data as Record<string, unknown> | undefined;
      const type = (d?.type as string) || m.kind;

      switch (type) {
        case 'input_transcript.done': {
          const text = (d?.transcript as string) || '';
          if (text) {
            result.push({ id: `u_${result.length}`, role: 'user', chunks: [text], complete: true });
            currentAssistant = null;
          }
          break;
        }
        case 'transcript.delta': {
          const text = (d?.content as string) || '';
          if (!currentAssistant) {
            currentAssistant = { id: `a_${result.length}`, role: 'assistant', chunks: [], complete: false };
            result.push(currentAssistant);
          }
          if (text) currentAssistant.chunks.push(text);
          break;
        }
        case 'transcript.done': {
          if (currentAssistant) {
            currentAssistant.complete = true;
            const full = (d?.transcript as string) || '';
            if (full && currentAssistant.chunks.length === 0) currentAssistant.chunks.push(full);
          } else {
            const full = (d?.transcript as string) || '';
            if (full) {
              result.push({ id: `a_${result.length}`, role: 'assistant', chunks: [full], complete: true });
            }
          }
          currentAssistant = null;
          break;
        }
        case 'audio.done': {
          const dur = d?.duration_ms as number;
          const last = result[result.length - 1];
          if (last && last.role === 'assistant' && dur) last.durationMs = dur;
          break;
        }
        case 'emotion': {
          const last = result[result.length - 1];
          if (last) {
            last.emotion = {
              state: (d?.state as string) || '',
              emotion: (d?.emotion as string) || '',
              intensity: (d?.intensity as number) || 0,
            };
          }
          break;
        }
      }
    }
    return result;
  }, [renderMessages]);

  useEffect(() => {
    endRef.current?.scrollIntoView({ behavior: 'smooth' });
  }, [entries.length]);

  return (
    <div style={panelStyle}>
      <div style={headerStyle}>
        <span style={{ fontWeight: 600, fontSize: 12 }}>Subtitles</span>
        {realtimeActive && <span style={{ color: '#28a745', fontSize: 11 }}>● Live</span>}
        <button onClick={onClose} style={closeBtn}>×</button>
      </div>

      {/* Waveform overlay when realtime is active */}
      {realtimeActive && (micAnalyser || playAnalyser) && (
        <div style={waveformBar}>
          <WaveformMini analyser={micAnalyser} color="#0066cc" label="IN" />
          <WaveformMini analyser={playAnalyser} color="#28a745" label="OUT" />
        </div>
      )}

      {/* Subtitle entries */}
      <div style={bodyStyle}>
        {entries.length === 0 && (
          <div style={{ color: '#999', fontSize: 12, textAlign: 'center', padding: 16 }}>
            {realtimeActive ? 'Waiting for speech...' : 'No subtitles yet'}
          </div>
        )}
        {entries.map((e) => (
          <div key={e.id} style={{ marginBottom: 6 }}>
            <div style={{
              display: 'flex', justifyContent: e.role === 'user' ? 'flex-end' : 'flex-start',
            }}>
              <div style={{
                maxWidth: '85%', padding: '4px 10px', borderRadius: 8, fontSize: 12, lineHeight: 1.5,
                background: e.role === 'user' ? '#0066cc' : '#f0f0f0',
                color: e.role === 'user' ? '#fff' : '#333',
              }}>
                {e.chunks.join('')}
                {!e.complete && <span style={{ opacity: 0.5 }}>▍</span>}
              </div>
            </div>
            {e.durationMs !== undefined && (
              <div style={{ fontSize: 10, color: '#999', textAlign: e.role === 'user' ? 'right' : 'left', marginTop: 1 }}>
                {(e.durationMs / 1000).toFixed(1)}s
              </div>
            )}
            {e.emotion && (
              <div style={{ fontSize: 10, color: '#888', textAlign: 'left', marginTop: 1 }}>
                {e.emotion.emotion} ({e.emotion.state}) [{e.emotion.intensity}]
              </div>
            )}
          </div>
        ))}
        <div ref={endRef} />
      </div>
    </div>
  );
}

function WaveformMini({ analyser, color, label }: { analyser: AnalyserNode | null; color: string; label: string }) {
  const canvasRef = useRef<HTMLCanvasElement>(null);

  useEffect(() => {
    const canvas = canvasRef.current;
    if (!canvas || !analyser) return;
    const ctx = canvas.getContext('2d')!;
    const bufLen = analyser.frequencyBinCount;
    const waveData = new Uint8Array(bufLen);
    let id: number;
    const draw = () => {
      id = requestAnimationFrame(draw);
      analyser.getByteTimeDomainData(waveData);
      const w = canvas.width;
      const h = canvas.height;
      ctx.clearRect(0, 0, w, h);
      ctx.lineWidth = 1.5;
      ctx.strokeStyle = color;
      ctx.beginPath();
      const step = w / bufLen;
      for (let i = 0; i < bufLen; i++) {
        const y = (waveData[i] / 128) * (h / 2);
        if (i === 0) ctx.moveTo(0, y); else ctx.lineTo(i * step, y);
      }
      ctx.lineTo(w, h / 2);
      ctx.stroke();
    };
    draw();
    return () => cancelAnimationFrame(id);
  }, [analyser, color]);

  if (!analyser) return null;
  return (
    <div style={{ flex: 1, display: 'flex', alignItems: 'center', gap: 4 }}>
      <span style={{ fontSize: 9, color, fontWeight: 600, width: 24 }}>{label}</span>
      <canvas ref={canvasRef} width={300} height={24} style={{ width: '100%', height: 24, borderRadius: 3, background: 'rgba(0,0,0,0.03)' }} />
    </div>
  );
}

const panelStyle: React.CSSProperties = {
  display: 'flex', flexDirection: 'column', minHeight: 0,
  border: '1px solid #e0e0e0', borderRadius: 4, background: '#fafafa',
};
const headerStyle: React.CSSProperties = {
  display: 'flex', alignItems: 'center', gap: 8, padding: '4px 8px',
  borderBottom: '1px solid #eee', flexShrink: 0,
};
const closeBtn: React.CSSProperties = {
  marginLeft: 'auto', background: 'none', border: 'none', cursor: 'pointer',
  color: '#999', fontSize: 14, padding: '0 2px',
};
const waveformBar: React.CSSProperties = {
  display: 'flex', gap: 8, padding: '4px 8px', borderBottom: '1px solid #eee',
  background: 'rgba(0,0,0,0.02)', flexShrink: 0,
};
const bodyStyle: React.CSSProperties = {
  flex: 1, overflow: 'auto', padding: '6px 8px',
};

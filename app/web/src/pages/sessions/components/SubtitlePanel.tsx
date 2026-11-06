import { useEffect, useRef, useMemo } from 'react';
import type { WSMessage } from '../../../hooks/useWebSocket';
import Button from '../../../components/ui/Button';

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
    <div className="subtitle panel">
      <div className="panel__header">
        <span>Subtitles</span>
        {realtimeActive && <span className="badge badge--success">● Live</span>}
        <span className="spacer" />
        <Button size="sm" variant="ghost" onClick={onClose}>×</Button>
      </div>

      {/* Waveform overlay when realtime is active */}
      {realtimeActive && (micAnalyser || playAnalyser) && (
        <div className="subtitle__waveforms">
          <WaveformMini analyser={micAnalyser} color="#4d9eff" label="IN" />
          <WaveformMini analyser={playAnalyser} color="#3fb950" label="OUT" />
        </div>
      )}

      {/* Subtitle entries */}
      <div className="panel__body" style={{ padding: 'var(--space-2)' }}>
        {entries.length === 0 && (
          <div className="faint" style={{ fontSize: 'var(--text-sm)', textAlign: 'center', padding: 'var(--space-4)' }}>
            {realtimeActive ? 'Waiting for speech…' : 'No subtitles yet'}
          </div>
        )}
        {entries.map((e) => (
          <div key={e.id} className={`subtitle__entry subtitle__entry--${e.role}`}>
            <div className="subtitle__bubble">
              {e.chunks.join('')}
              {!e.complete && <span style={{ opacity: 0.5 }}>▍</span>}
            </div>
            {e.durationMs !== undefined && (
              <div className="subtitle__meta mono">
                {(e.durationMs / 1000).toFixed(1)}s
              </div>
            )}
            {e.emotion && (
              <div className="subtitle__meta mono">
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
    <div className="subtitle__wave">
      <span className="subtitle__wave-label mono" style={{ color }}>{label}</span>
      <canvas ref={canvasRef} width={300} height={24} className="subtitle__canvas" />
    </div>
  );
}

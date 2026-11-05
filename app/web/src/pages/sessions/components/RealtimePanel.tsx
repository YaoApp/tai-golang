import { useState, useRef, useCallback, useEffect } from 'react';
import type { WSMessage } from '../../../hooks/useWebSocket';

interface Props {
  sessionId: string;
  ctrlConnected: boolean;
  ctrlMessages: WSMessage[];
  onSendCtrl: (msg: WSMessage) => void;
  onRealtimeChange: (active: boolean) => void;
}

type RealtimeStatus = 'stopped' | 'starting' | 'running' | 'stopping';

export default function RealtimePanel({
  sessionId, ctrlConnected, ctrlMessages, onSendCtrl, onRealtimeChange,
}: Props) {
  const [status, setStatus] = useState<RealtimeStatus>('stopped');
  const [provider, setProvider] = useState('openai');
  const [model, setModel] = useState('gpt-realtime-mini');
  const [apiKey, setApiKey] = useState('');
  const [voice, setVoice] = useState('alloy');
  const [motionEnabled, setMotionEnabled] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [micAnalyser, setMicAnalyser] = useState<AnalyserNode | null>(null);
  const [playAnalyser, setPlayAnalyser] = useState<AnalyserNode | null>(null);

  const audioWsRef = useRef<WebSocket | null>(null);
  const mediaStreamRef = useRef<MediaStream | null>(null);
  const captureCtxRef = useRef<AudioContext | null>(null);
  const playCtxRef = useRef<AudioContext | null>(null);
  const captureProcessorRef = useRef<ScriptProcessorNode | null>(null);
  const playProcessorRef = useRef<ScriptProcessorNode | null>(null);
  const playbackQueueRef = useRef<Float32Array[]>([]);
  const playbackOffsetRef = useRef(0);
  const lastCtrlIdxRef = useRef(0);
  const startTimerRef = useRef<ReturnType<typeof setTimeout> | null>(null);
  const statusRef = useRef<RealtimeStatus>('stopped');
  statusRef.current = status;

  const audioGenRef = useRef(0);

  const cleanupAudio = useCallback(() => {
    audioGenRef.current++;
    if (captureProcessorRef.current) {
      captureProcessorRef.current.disconnect();
      captureProcessorRef.current = null;
    }
    if (playProcessorRef.current) {
      playProcessorRef.current.disconnect();
      playProcessorRef.current = null;
    }
    playbackQueueRef.current = [];
    playbackOffsetRef.current = 0;
    if (mediaStreamRef.current) {
      mediaStreamRef.current.getTracks().forEach((t) => t.stop());
      mediaStreamRef.current = null;
    }
    if (captureCtxRef.current) {
      captureCtxRef.current.close().catch(() => {});
      captureCtxRef.current = null;
    }
    if (playCtxRef.current) {
      playCtxRef.current.close().catch(() => {});
      playCtxRef.current = null;
    }
    if (audioWsRef.current) {
      audioWsRef.current.close();
      audioWsRef.current = null;
    }
    setMicAnalyser(null);
    setPlayAnalyser(null);
  }, []);

  const connectAudio = useCallback(async () => {
    const myGen = ++audioGenRef.current;
    try {
      const protocol = window.location.protocol === 'https:' ? 'wss:' : 'ws:';
      const wsUrl = `${protocol}//${window.location.host}/ws/sessions/${sessionId}/audio`;
      const ws = new WebSocket(wsUrl);
      ws.binaryType = 'arraybuffer';
      audioWsRef.current = ws;

      // --- Playback: queued PCM output ---
      const playCtx = new AudioContext({ sampleRate: 24000 });
      playCtxRef.current = playCtx;

      const queue = playbackQueueRef.current;
      const playProcessor = playCtx.createScriptProcessor(2048, 0, 1);
      playProcessorRef.current = playProcessor;
      playProcessor.onaudioprocess = (ev) => {
        const output = ev.outputBuffer.getChannelData(0);
        let written = 0;
        while (written < output.length && queue.length > 0) {
          const current = queue[0];
          const remaining = current.length - playbackOffsetRef.current;
          const toCopy = Math.min(remaining, output.length - written);
          output.set(current.subarray(playbackOffsetRef.current, playbackOffsetRef.current + toCopy), written);
          written += toCopy;
          playbackOffsetRef.current += toCopy;
          if (playbackOffsetRef.current >= current.length) {
            queue.shift();
            playbackOffsetRef.current = 0;
          }
        }
        if (written < output.length) {
          output.fill(0, written);
        }
      };
      playProcessor.connect(playCtx.destination);

      // Playback analyser (output waveform)
      const pAnalyser = playCtx.createAnalyser();
      pAnalyser.fftSize = 256;
      playProcessor.connect(pAnalyser);

      ws.onmessage = (e) => {
        if (audioGenRef.current !== myGen) return;
        if (!(e.data instanceof ArrayBuffer)) return;
        const pcm16 = new Int16Array(e.data);
        const float32 = new Float32Array(pcm16.length);
        for (let i = 0; i < pcm16.length; i++) float32[i] = pcm16[i] / 32768;
        queue.push(float32);
      };

      // --- Capture: microphone → PCM → server ---
      const stream = await navigator.mediaDevices.getUserMedia({
        audio: { sampleRate: 24000, channelCount: 1 },
      });

      if (audioGenRef.current !== myGen) {
        stream.getTracks().forEach((t) => t.stop());
        playCtx.close().catch(() => {});
        playProcessor.disconnect();
        ws.close();
        return;
      }
      mediaStreamRef.current = stream;

      const captureCtx = new AudioContext({ sampleRate: 24000 });
      captureCtxRef.current = captureCtx;
      const source = captureCtx.createMediaStreamSource(stream);

      const captureProcessor = captureCtx.createScriptProcessor(4096, 1, 1);
      captureProcessorRef.current = captureProcessor;
      captureProcessor.onaudioprocess = (ev) => {
        if (audioGenRef.current !== myGen) return;
        if (ws.readyState !== WebSocket.OPEN) return;
        const input = ev.inputBuffer.getChannelData(0);
        const pcm16 = new Int16Array(input.length);
        for (let i = 0; i < input.length; i++) {
          pcm16[i] = Math.max(-32768, Math.min(32767, Math.round(input[i] * 32767)));
        }
        ws.send(pcm16.buffer);
      };
      source.connect(captureProcessor);
      captureProcessor.connect(captureCtx.destination);

      // Mic analyser (input waveform)
      const mAnalyser = captureCtx.createAnalyser();
      mAnalyser.fftSize = 256;
      source.connect(mAnalyser);

      if (audioGenRef.current !== myGen) {
        captureProcessor.disconnect();
        playProcessor.disconnect();
        stream.getTracks().forEach((t) => t.stop());
        captureCtx.close().catch(() => {});
        playCtx.close().catch(() => {});
        ws.close();
        return;
      }
      setMicAnalyser(mAnalyser);
      setPlayAnalyser(pAnalyser);
    } catch (err) {
      if (audioGenRef.current !== myGen) return;
      setError(`Microphone: ${err instanceof Error ? err.message : String(err)}`);
    }
  }, [sessionId]);

  // Watch ctrl messages for realtime lifecycle events.
  useEffect(() => {
    if (ctrlMessages.length <= lastCtrlIdxRef.current) return;
    const incoming = ctrlMessages.slice(lastCtrlIdxRef.current);
    lastCtrlIdxRef.current = ctrlMessages.length;

    for (const msg of incoming) {
      const d = msg.data as Record<string, unknown> | undefined;
      switch (msg.kind) {
        case 'realtime.started':
          if (startTimerRef.current) { clearTimeout(startTimerRef.current); startTimerRef.current = null; }
          setError(null);
          setStatus('running');
          onRealtimeChange(true);
          connectAudio();
          break;

        case 'realtime.stopped':
          if (startTimerRef.current) { clearTimeout(startTimerRef.current); startTimerRef.current = null; }
          cleanupAudio();
          if (statusRef.current !== 'stopped') setStatus('stopped');
          onRealtimeChange(false);
          break;

        case 'realtime.error': {
          if (startTimerRef.current) { clearTimeout(startTimerRef.current); startTimerRef.current = null; }
          const m = (d?.message as string) || (d?.code as string) || 'Realtime error';
          setError(m);
          cleanupAudio();
          if (statusRef.current !== 'stopped') setStatus('stopped');
          onRealtimeChange(false);
          break;
        }

        case 'error': {
          const m = (d?.message as string) || '';
          if (statusRef.current === 'starting' && (m.includes('realtime') || m.includes('start_realtime'))) {
            if (startTimerRef.current) { clearTimeout(startTimerRef.current); startTimerRef.current = null; }
            setError(m);
            cleanupAudio();
            setStatus('stopped');
            onRealtimeChange(false);
          }
          break;
        }
      }
    }
  }, [ctrlMessages.length, cleanupAudio, connectAudio, onRealtimeChange]);

  const startRealtime = useCallback(() => {
    setStatus('starting');
    setError(null);
    onSendCtrl({
      kind: 'start_realtime',
      data: {
        providers: {
          primary: { provider, model, api_key: apiKey || undefined },
        },
        audio: {
          input_format: 'pcm16',
          output_format: 'pcm16',
          sample_rate: 24000,
          channels: 1,
          voice,
        },
        motion: motionEnabled ? { enabled: true, provider: 'fast', fps: 30 } : undefined,
      },
    });
    if (startTimerRef.current) clearTimeout(startTimerRef.current);
    startTimerRef.current = setTimeout(() => {
      if (statusRef.current === 'starting') {
        setError('Connection timeout — realtime provider did not respond within 15 s');
        setStatus('stopped');
        onRealtimeChange(false);
      }
    }, 15000);
  }, [provider, model, apiKey, voice, motionEnabled, onSendCtrl, onRealtimeChange]);

  const stopRealtime = useCallback(() => {
    setStatus('stopping');
    onSendCtrl({ kind: 'stop_realtime' });
    cleanupAudio();
    setTimeout(() => {
      if (statusRef.current === 'stopping') {
        setStatus('stopped');
        onRealtimeChange(false);
      }
    }, 3000);
  }, [onSendCtrl, cleanupAudio, onRealtimeChange]);

  useEffect(() => () => {
    cleanupAudio();
    if (startTimerRef.current) clearTimeout(startTimerRef.current);
  }, [cleanupAudio]);

  return (
    <div style={panelStyle}>
      <h3 style={{ margin: '0 0 12px', fontSize: 14 }}>Realtime Audio</h3>

      {error && (
        <div style={errorBanner}>
          <span>⚠️</span>
          <span style={{ flex: 1, wordBreak: 'break-word' }}>{error}</span>
          <button onClick={() => setError(null)} style={dismissBtn}>×</button>
        </div>
      )}

      {status === 'stopped' && (
        <div style={{ display: 'flex', flexDirection: 'column', gap: 8 }}>
          <div style={{ display: 'flex', gap: 8, alignItems: 'center', flexWrap: 'wrap' }}>
            <label style={labelStyle}>
              Provider:
              <select value={provider} onChange={(e) => setProvider(e.target.value)} style={inputStyle}>
                <option value="openai">OpenAI</option>
                <option value="qwen">Qwen</option>
              </select>
            </label>
            <label style={labelStyle}>
              Model:
              <input value={model} onChange={(e) => setModel(e.target.value)} style={{ ...inputStyle, width: 200 }} />
            </label>
            <label style={labelStyle}>
              Voice:
              <select value={voice} onChange={(e) => setVoice(e.target.value)} style={inputStyle}>
                <option value="alloy">Alloy</option>
                <option value="echo">Echo</option>
                <option value="shimmer">Shimmer</option>
                <option value="ash">Ash</option>
                <option value="coral">Coral</option>
                <option value="sage">Sage</option>
              </select>
            </label>
          </div>
          <div style={{ display: 'flex', gap: 8, alignItems: 'center' }}>
            <label style={labelStyle}>
              API Key:
              <input
                type="password"
                value={apiKey}
                onChange={(e) => setApiKey(e.target.value)}
                placeholder="Optional (uses server env)"
                style={{ ...inputStyle, width: 240 }}
              />
            </label>
            <label style={{ ...labelStyle, cursor: 'pointer' }}>
              <input type="checkbox" checked={motionEnabled} onChange={(e) => setMotionEnabled(e.target.checked)} />
              {' '}Motion
            </label>
          </div>
          <button onClick={startRealtime} disabled={!ctrlConnected} style={startBtnS(ctrlConnected)}>
            Start Realtime
          </button>
        </div>
      )}

      {status === 'running' && (
        <div style={{ display: 'flex', flexDirection: 'column', gap: 8 }}>
          <div style={{ display: 'flex', gap: 8, alignItems: 'center' }}>
            <span style={{ color: '#28a745', fontWeight: 600, fontSize: 13 }}>● Connected</span>
            {micAnalyser && <span style={{ color: '#0066cc', fontSize: 12 }}>🎤 Mic active</span>}
            <button onClick={() => onSendCtrl({ kind: 'audio_commit' })} style={actionBtn}>Commit</button>
            <button onClick={() => onSendCtrl({ kind: 'audio_interrupt' })} style={actionBtn}>Interrupt</button>
            <button onClick={stopRealtime} style={stopBtnS}>Stop</button>
          </div>
          <div style={{ display: 'flex', gap: 8 }}>
            <div style={{ flex: 1 }}>
              <span style={{ fontSize: 11, color: '#0066cc', fontWeight: 500 }}>🎤 Input</span>
              <Waveform analyser={micAnalyser} color="#0066cc" />
            </div>
            <div style={{ flex: 1 }}>
              <span style={{ fontSize: 11, color: '#28a745', fontWeight: 500 }}>🔊 Output</span>
              <Waveform analyser={playAnalyser} color="#28a745" />
            </div>
          </div>
        </div>
      )}

      {(status === 'starting' || status === 'stopping') && (
        <span style={{ color: '#666', fontSize: 13 }}>
          {status === 'starting' ? '⏳ Connecting to realtime provider...' : '⏳ Stopping...'}
        </span>
      )}
    </div>
  );
}

/* ---- Waveform visualizer ---- */

function Waveform({ analyser, color }: { analyser: AnalyserNode | null; color: string }) {
  const canvasRef = useRef<HTMLCanvasElement>(null);

  useEffect(() => {
    const canvas = canvasRef.current;
    if (!canvas || !analyser) return;
    const ctx = canvas.getContext('2d')!;
    const bufLen = analyser.frequencyBinCount;
    const data = new Uint8Array(bufLen);
    let id: number;

    const draw = () => {
      id = requestAnimationFrame(draw);
      analyser.getByteTimeDomainData(data);

      const w = canvas.width;
      const h = canvas.height;
      ctx.fillStyle = '#f0f4f8';
      ctx.fillRect(0, 0, w, h);

      ctx.lineWidth = 2;
      ctx.strokeStyle = color;
      ctx.beginPath();

      const step = w / bufLen;
      for (let i = 0; i < bufLen; i++) {
        const y = (data[i] / 128) * (h / 2);
        if (i === 0) ctx.moveTo(0, y);
        else ctx.lineTo(i * step, y);
      }
      ctx.lineTo(w, h / 2);
      ctx.stroke();
    };
    draw();
    return () => cancelAnimationFrame(id);
  }, [analyser, color]);

  if (!analyser) return null;
  return (
    <canvas
      ref={canvasRef}
      width={600}
      height={48}
      style={{ width: '100%', height: 48, borderRadius: 4, background: '#f0f4f8' }}
    />
  );
}

/* ---- styles ---- */

const panelStyle: React.CSSProperties = {
  border: '1px solid #e0e0e0', borderRadius: 8, padding: 16,
  marginBottom: 16, background: '#f9f9f9',
};

const errorBanner: React.CSSProperties = {
  background: '#fef2f2', border: '1px solid #fca5a5', borderRadius: 6,
  padding: '8px 12px', marginBottom: 12, fontSize: 12, color: '#991b1b',
  display: 'flex', alignItems: 'flex-start', gap: 8,
};

const dismissBtn: React.CSSProperties = {
  background: 'none', border: 'none', cursor: 'pointer',
  color: '#991b1b', fontSize: 18, lineHeight: '1', padding: 0,
};

const labelStyle: React.CSSProperties = {
  fontSize: 12, display: 'flex', alignItems: 'center', gap: 4,
};

const inputStyle: React.CSSProperties = {
  padding: '4px 8px', border: '1px solid #ccc', borderRadius: 4, fontSize: 12,
};

const startBtnS = (ok: boolean): React.CSSProperties => ({
  padding: '8px 16px', border: 'none', borderRadius: 4, fontSize: 13,
  background: ok ? '#28a745' : '#ccc', color: '#fff',
  cursor: ok ? 'pointer' : 'default', alignSelf: 'flex-start',
});

const actionBtn: React.CSSProperties = {
  padding: '6px 12px', border: '1px solid #666', borderRadius: 4,
  background: 'transparent', color: '#333', cursor: 'pointer', fontSize: 12,
};

const stopBtnS: React.CSSProperties = {
  padding: '6px 12px', border: '1px solid #dc3545', borderRadius: 4,
  background: 'transparent', color: '#dc3545', cursor: 'pointer', fontSize: 12,
};

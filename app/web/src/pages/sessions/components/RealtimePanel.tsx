import { useState, useRef, useCallback, useEffect } from 'react';
import type { WSMessage } from '../../../hooks/useWebSocket';
import Button from '../../../components/ui/Button';

interface Props {
  sessionId: string;
  ctrlConnected: boolean;
  ctrlMessages: WSMessage[];
  onSendCtrl: (msg: WSMessage) => void;
  onRealtimeChange: (active: boolean, motionEnabled?: boolean) => void;
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
  const [motionPreset, setMotionPreset] = useState('fast');
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
          onRealtimeChange(true, motionEnabled);
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
  }, [ctrlMessages.length, cleanupAudio, connectAudio, onRealtimeChange, motionEnabled]);

  const startRealtime = useCallback(() => {
    setStatus('starting');
    setError(null);
    onSendCtrl({
      kind: 'start_realtime',
      data: {
        providers: {
          // Wire names follow the Go struct tags in types.Provider; sending
          // `provider` here leaves ProviderID empty and the node rejects the
          // start with "requires provider and model".
          primary: { provider_id: provider, model, api_key: apiKey || undefined },
        },
        audio: {
          input_format: 'pcm16',
          output_format: 'pcm16',
          sample_rate: 24000,
          channels: 1,
          voice,
        },
        motion: motionEnabled ? { enabled: true, provider: motionPreset, fps: 30 } : undefined,
      },
      receivedAt: Date.now(),
    });
    if (startTimerRef.current) clearTimeout(startTimerRef.current);
    startTimerRef.current = setTimeout(() => {
      if (statusRef.current === 'starting') {
        setError('Connection timeout — realtime provider did not respond within 15 s');
        setStatus('stopped');
        onRealtimeChange(false);
      }
    }, 15000);
  }, [provider, model, apiKey, voice, motionEnabled, motionPreset, onSendCtrl, onRealtimeChange]);

  const stopRealtime = useCallback(() => {
    setStatus('stopping');
    onSendCtrl({ kind: 'stop_realtime', receivedAt: Date.now() });
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
    <div className="rt-panel" style={{ marginBottom: 'var(--space-4)' }}>
      <div className="section-title" style={{ margin: '0 0 var(--space-3)' }}>Realtime Audio</div>

      {error && (
        <div className="notice notice--error" style={{ marginBottom: 'var(--space-3)' }}>
          <span style={{ flex: 1, wordBreak: 'break-word' }}>{error}</span>
          <Button size="sm" variant="ghost" onClick={() => setError(null)}>×</Button>
        </div>
      )}

      {status === 'stopped' && (
        <div className="stack" style={{ gap: 'var(--space-2)' }}>
          <div className="row row--wrap">
            <label className="rt-field">
              <span className="field__label">Provider</span>
              <select className="select" value={provider} onChange={(e) => setProvider(e.target.value)}>
                <option value="openai">OpenAI</option>
                <option value="qwen">Qwen</option>
              </select>
            </label>
            <label className="rt-field" style={{ flex: 1 }}>
              <span className="field__label">Model</span>
              <input className="input mono" value={model} onChange={(e) => setModel(e.target.value)} />
            </label>
            <label className="rt-field">
              <span className="field__label">Voice</span>
              <select className="select" value={voice} onChange={(e) => setVoice(e.target.value)}>
                <option value="alloy">Alloy</option>
                <option value="echo">Echo</option>
                <option value="shimmer">Shimmer</option>
                <option value="ash">Ash</option>
                <option value="coral">Coral</option>
                <option value="sage">Sage</option>
              </select>
            </label>
          </div>

          <div className="row row--wrap">
            <label className="rt-field" style={{ flex: 1 }}>
              <span className="field__label">API Key</span>
              <input
                className="input mono"
                type="password"
                value={apiKey}
                onChange={(e) => setApiKey(e.target.value)}
                placeholder="Optional (uses server env)"
              />
            </label>
            <label className="rt-check">
              <input type="checkbox" checked={motionEnabled} onChange={(e) => setMotionEnabled(e.target.checked)} />
              Motion
            </label>
            {motionEnabled && (
              <select className="select" value={motionPreset} onChange={(e) => setMotionPreset(e.target.value)}>
                <option value="fast">fast</option>
                <option value="balanced">balanced</option>
              </select>
            )}
          </div>

          <Button variant="primary" onClick={startRealtime} disabled={!ctrlConnected} style={{ alignSelf: 'flex-start' }}>
            Start Realtime
          </Button>
        </div>
      )}

      {status === 'running' && (
        <div className="stack" style={{ gap: 'var(--space-2)' }}>
          <div className="row row--wrap">
            <span className="rt-running">
              <span className="chat__dot chat__dot--on" />
              Connected
            </span>
            {micAnalyser && <span className="badge badge--accent">mic active</span>}
            <Button size="sm" onClick={() => onSendCtrl({ kind: 'audio_commit', receivedAt: Date.now() })}>Commit</Button>
            <Button size="sm" onClick={() => onSendCtrl({ kind: 'audio_interrupt', receivedAt: Date.now() })}>Interrupt</Button>
            <Button size="sm" variant="danger" onClick={stopRealtime}>Stop</Button>
          </div>

          <div className="row" style={{ gap: 'var(--space-2)', alignItems: 'flex-start' }}>
            <div style={{ flex: 1, minWidth: 0 }}>
              <span className="field__label" style={{ color: 'var(--accent)' }}>Input</span>
              <Waveform analyser={micAnalyser} color="#4d9eff" />
            </div>
            <div style={{ flex: 1, minWidth: 0 }}>
              <span className="field__label" style={{ color: 'var(--success)' }}>Output</span>
              <Waveform analyser={playAnalyser} color="#3fb950" />
            </div>
          </div>
        </div>
      )}

      {(status === 'starting' || status === 'stopping') && (
        <span className="faint" style={{ fontSize: 'var(--text-sm)' }}>
          {status === 'starting' ? 'Connecting to realtime provider…' : 'Stopping…'}
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
      ctx.fillStyle = '#0b0e14';
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
    <canvas ref={canvasRef} width={600} height={48} className="rt-wave" />
  );
}

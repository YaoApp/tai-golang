import { useState, useRef, useCallback, useEffect } from 'react';
import type { WSMessage } from '../../../hooks/useWebSocket';

export interface AudioStats {
  inFrames: number;
  outFrames: number;
  inBytes: number;
  outBytes: number;
}

interface Props {
  sessionId: string;
  ctrlConnected: boolean;
  ctrlMessages: WSMessage[];
  onSendCtrl: (msg: WSMessage) => void;
  onRealtimeChange: (active: boolean) => void;
  onAnalysers: (mic: AnalyserNode | null, play: AnalyserNode | null) => void;
  onAudioStats: (stats: AudioStats) => void;
}

type RealtimeStatus = 'stopped' | 'starting' | 'running' | 'stopping';

export default function RealtimeOverlay({
  sessionId, ctrlConnected, ctrlMessages, onSendCtrl, onRealtimeChange, onAnalysers, onAudioStats,
}: Props) {
  const [status, setStatus] = useState<RealtimeStatus>('stopped');
  const [provider, setProvider] = useState('openai');
  const [model, setModel] = useState('gpt-realtime-mini');
  const [voice, setVoice] = useState('alloy');
  const [motionEnabled, setMotionEnabled] = useState(false);
  const [motionPreset, setMotionPreset] = useState('fast');
  const [error, setError] = useState<string | null>(null);

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
  const audioStatsRef = useRef({ inFrames: 0, outFrames: 0, inBytes: 0, outBytes: 0 });

  const cleanupAudio = useCallback(() => {
    audioGenRef.current++;
    if (captureProcessorRef.current) { captureProcessorRef.current.disconnect(); captureProcessorRef.current = null; }
    if (playProcessorRef.current) { playProcessorRef.current.disconnect(); playProcessorRef.current = null; }
    playbackQueueRef.current = [];
    playbackOffsetRef.current = 0;
    if (mediaStreamRef.current) { mediaStreamRef.current.getTracks().forEach((t) => t.stop()); mediaStreamRef.current = null; }
    if (captureCtxRef.current) { captureCtxRef.current.close().catch(() => {}); captureCtxRef.current = null; }
    if (playCtxRef.current) { playCtxRef.current.close().catch(() => {}); playCtxRef.current = null; }
    if (audioWsRef.current) { audioWsRef.current.close(); audioWsRef.current = null; }
    onAnalysers(null, null);
  }, [onAnalysers]);

  const connectAudio = useCallback(async () => {
    const myGen = ++audioGenRef.current;
    try {
      const protocol = window.location.protocol === 'https:' ? 'wss:' : 'ws:';
      const wsUrl = `${protocol}//${window.location.host}/ws/sessions/${sessionId}/audio`;
      const ws = new WebSocket(wsUrl);
      ws.binaryType = 'arraybuffer';
      audioWsRef.current = ws;

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
          if (playbackOffsetRef.current >= current.length) { queue.shift(); playbackOffsetRef.current = 0; }
        }
        if (written < output.length) output.fill(0, written);
      };
      playProcessor.connect(playCtx.destination);

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
        audioStatsRef.current.inFrames++;
        audioStatsRef.current.inBytes += e.data.byteLength;
        onAudioStats({ ...audioStatsRef.current });
      };

      const stream = await navigator.mediaDevices.getUserMedia({ audio: { sampleRate: 24000, channelCount: 1 } });
      if (audioGenRef.current !== myGen) {
        stream.getTracks().forEach((t) => t.stop()); playCtx.close().catch(() => {}); playProcessor.disconnect(); ws.close();
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
        audioStatsRef.current.outFrames++;
        audioStatsRef.current.outBytes += pcm16.buffer.byteLength;
        onAudioStats({ ...audioStatsRef.current });
      };
      source.connect(captureProcessor);
      captureProcessor.connect(captureCtx.destination);

      const mAnalyser = captureCtx.createAnalyser();
      mAnalyser.fftSize = 256;
      source.connect(mAnalyser);

      if (audioGenRef.current !== myGen) {
        captureProcessor.disconnect(); playProcessor.disconnect();
        stream.getTracks().forEach((t) => t.stop());
        captureCtx.close().catch(() => {}); playCtx.close().catch(() => {});
        ws.close();
        return;
      }
      onAnalysers(mAnalyser, pAnalyser);
    } catch (err) {
      if (audioGenRef.current !== myGen) return;
      setError(`Microphone: ${err instanceof Error ? err.message : String(err)}`);
    }
  }, [sessionId, onAnalysers, onAudioStats]);

  useEffect(() => {
    if (ctrlMessages.length <= lastCtrlIdxRef.current) return;
    const incoming = ctrlMessages.slice(lastCtrlIdxRef.current);
    lastCtrlIdxRef.current = ctrlMessages.length;
    for (const msg of incoming) {
      const d = msg.data as Record<string, unknown> | undefined;
      switch (msg.kind) {
        case 'realtime.started':
          if (startTimerRef.current) { clearTimeout(startTimerRef.current); startTimerRef.current = null; }
          setError(null); setStatus('running'); onRealtimeChange(true); connectAudio();
          break;
        case 'realtime.stopped':
          if (startTimerRef.current) { clearTimeout(startTimerRef.current); startTimerRef.current = null; }
          cleanupAudio(); if (statusRef.current !== 'stopped') setStatus('stopped'); onRealtimeChange(false);
          break;
        case 'realtime.error': {
          if (startTimerRef.current) { clearTimeout(startTimerRef.current); startTimerRef.current = null; }
          setError((d?.message as string) || (d?.code as string) || 'Realtime error');
          cleanupAudio(); if (statusRef.current !== 'stopped') setStatus('stopped'); onRealtimeChange(false);
          break;
        }
        case 'error': {
          const m = (d?.message as string) || '';
          if (statusRef.current === 'starting' && (m.includes('realtime') || m.includes('start_realtime'))) {
            if (startTimerRef.current) { clearTimeout(startTimerRef.current); startTimerRef.current = null; }
            setError(m); cleanupAudio(); setStatus('stopped'); onRealtimeChange(false);
          }
          break;
        }
      }
    }
  }, [ctrlMessages.length, cleanupAudio, connectAudio, onRealtimeChange]);

  const startRealtime = useCallback(() => {
    setStatus('starting'); setError(null);
    onSendCtrl({
      kind: 'start_realtime',
      data: {
        providers: { primary: { provider, model } },
        audio: { input_format: 'pcm16', output_format: 'pcm16', sample_rate: 24000, channels: 1, voice },
        motion: motionEnabled ? { enabled: true, provider: motionPreset, fps: 30 } : undefined,
      },
      receivedAt: Date.now(),
    });
    if (startTimerRef.current) clearTimeout(startTimerRef.current);
    startTimerRef.current = setTimeout(() => {
      if (statusRef.current === 'starting') {
        setError('Connection timeout (15s)'); setStatus('stopped'); onRealtimeChange(false);
      }
    }, 15000);
  }, [provider, model, voice, motionEnabled, motionPreset, onSendCtrl, onRealtimeChange]);

  const stopRealtime = useCallback(() => {
    setStatus('stopping');
    onSendCtrl({ kind: 'stop_realtime', receivedAt: Date.now() });
    cleanupAudio();
    setTimeout(() => {
      if (statusRef.current === 'stopping') { setStatus('stopped'); onRealtimeChange(false); }
    }, 3000);
  }, [onSendCtrl, cleanupAudio, onRealtimeChange]);

  useEffect(() => () => {
    cleanupAudio();
    if (startTimerRef.current) clearTimeout(startTimerRef.current);
  }, [cleanupAudio]);

  return (
    <div>
      {error && (
        <div style={errorBanner}>
          <span>⚠️ {error}</span>
          <button onClick={() => setError(null)} style={dismissBtn}>×</button>
        </div>
      )}

      {status === 'stopped' && (
        <div style={{ display: 'flex', flexDirection: 'column', gap: 8 }}>
          <div style={{ display: 'flex', gap: 8, alignItems: 'center', flexWrap: 'wrap' }}>
            <label style={labelS}>Provider: <select value={provider} onChange={(e) => setProvider(e.target.value)} style={inputS}>
              <option value="openai">OpenAI</option><option value="qwen">Qwen</option>
            </select></label>
            <label style={labelS}>Model: <input value={model} onChange={(e) => setModel(e.target.value)} style={{ ...inputS, width: 160 }} /></label>
            <label style={labelS}>Voice: <select value={voice} onChange={(e) => setVoice(e.target.value)} style={inputS}>
              {['alloy','echo','shimmer','ash','coral','sage'].map((v) => <option key={v} value={v}>{v}</option>)}
            </select></label>
          </div>
          <div style={{ display: 'flex', gap: 8, alignItems: 'center' }}>
            <label style={{ ...labelS, cursor: 'pointer' }}>
              <input type="checkbox" checked={motionEnabled} onChange={(e) => setMotionEnabled(e.target.checked)} /> Motion
            </label>
            {motionEnabled && (
              <select value={motionPreset} onChange={(e) => setMotionPreset(e.target.value)} style={inputS}>
                <option value="fast">fast</option>
                <option value="balanced">balanced</option>
              </select>
            )}
          </div>
          <button onClick={startRealtime} disabled={!ctrlConnected} style={startBtn(ctrlConnected)}>Start Realtime</button>
        </div>
      )}

      {status === 'running' && (
        <div style={{ display: 'flex', gap: 8, alignItems: 'center', flexWrap: 'wrap' }}>
          <span style={{ color: '#28a745', fontWeight: 600, fontSize: 13 }}>● Connected</span>
          <button onClick={() => onSendCtrl({ kind: 'audio_commit', receivedAt: Date.now() })} style={actionBtn}>Commit</button>
          <button onClick={() => onSendCtrl({ kind: 'audio_interrupt', receivedAt: Date.now() })} style={actionBtn}>Interrupt</button>
          <button onClick={stopRealtime} style={stopBtn}>Stop</button>
        </div>
      )}

      {(status === 'starting' || status === 'stopping') && (
        <span style={{ color: '#666', fontSize: 13 }}>
          {status === 'starting' ? '⏳ Connecting...' : '⏳ Stopping...'}
        </span>
      )}
    </div>
  );
}

const errorBanner: React.CSSProperties = {
  background: '#fef2f2', border: '1px solid #fca5a5', borderRadius: 6,
  padding: '6px 10px', marginBottom: 8, fontSize: 12, color: '#991b1b',
  display: 'flex', alignItems: 'center', justifyContent: 'space-between', gap: 8,
};
const dismissBtn: React.CSSProperties = { background: 'none', border: 'none', cursor: 'pointer', color: '#991b1b', fontSize: 16, padding: 0 };
const labelS: React.CSSProperties = { fontSize: 12, display: 'flex', alignItems: 'center', gap: 4 };
const inputS: React.CSSProperties = { padding: '3px 6px', border: '1px solid #ccc', borderRadius: 4, fontSize: 12 };
const startBtn = (ok: boolean): React.CSSProperties => ({
  padding: '7px 14px', border: 'none', borderRadius: 4, fontSize: 13, fontWeight: 600,
  background: ok ? '#28a745' : '#ccc', color: '#fff', cursor: ok ? 'pointer' : 'default', alignSelf: 'flex-start',
});
const actionBtn: React.CSSProperties = { padding: '5px 10px', border: '1px solid #666', borderRadius: 4, background: 'transparent', color: '#333', cursor: 'pointer', fontSize: 12 };
const stopBtn: React.CSSProperties = { padding: '5px 10px', border: '1px solid #dc3545', borderRadius: 4, background: 'transparent', color: '#dc3545', cursor: 'pointer', fontSize: 12 };

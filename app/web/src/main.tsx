import { StrictMode } from 'react'
import { createRoot } from 'react-dom/client'
import App from './App'

// Minimal global styles
const style = document.createElement('style');
style.textContent = `
  * { box-sizing: border-box; margin: 0; padding: 0; }
  body { font-family: -apple-system, BlinkMacSystemFont, 'Segoe UI', Roboto, sans-serif; color: #333; }
  a { color: #0066cc; }
  code { background: #f1f3f5; padding: 2px 5px; border-radius: 3px; font-size: 13px; }
  pre { background: #f1f3f5; padding: 8px 12px; border-radius: 4px; overflow-x: auto; }
`;
document.head.appendChild(style);

createRoot(document.getElementById('root')!).render(
  <StrictMode>
    <App />
  </StrictMode>,
)

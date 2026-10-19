import '@fontsource-variable/geist';
import '@fontsource-variable/geist-mono';
import './styles/theme.css';

import { StrictMode } from 'react';
import { createRoot } from 'react-dom/client';

import App from './App';

const root = document.getElementById('root');

if (root === null) {
  throw new Error('index.html has no #root element');
}

createRoot(root).render(
  <StrictMode>
    <App />
  </StrictMode>,
);

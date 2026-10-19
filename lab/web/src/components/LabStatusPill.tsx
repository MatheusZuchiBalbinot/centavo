import type { LabReading } from '../hooks/useLabState';
import { messages } from '../i18n/pt-BR';
import styles from './LabStatusPill.module.css';

type LabStatusPillProps = { readonly reading: LabReading };

export default function LabStatusPill({ reading }: LabStatusPillProps): React.JSX.Element {
  const { text, tone } = describe(reading);

  return (
    <p className={styles.pill} data-tone={tone} role="status">
      {text}
    </p>
  );
}

type Tone = 'free' | 'busy' | 'unknown';

function describe(reading: LabReading): { text: string; tone: Tone } {
  switch (reading.kind) {
    case 'loading':
      return { text: messages.labStatus.unknown, tone: 'unknown' };
    case 'offline':
      return { text: messages.labStatus.offline, tone: 'unknown' };
    case 'ready':
      return describeState(reading.state.status);
  }
}

function describeState(status: 'idle' | 'running' | 'stopping'): { text: string; tone: Tone } {
  switch (status) {
    case 'idle':
      return { text: messages.labStatus.idle, tone: 'free' };
    case 'running':
      return { text: messages.labStatus.running, tone: 'busy' };
    case 'stopping':
      return { text: messages.labStatus.stopping, tone: 'busy' };
  }
}

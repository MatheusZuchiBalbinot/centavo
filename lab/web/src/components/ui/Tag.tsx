import styles from './Tag.module.css';

export type TagTone = 'neutral' | 'ok' | 'broken' | 'running';

type TagProps = { readonly tone: TagTone; readonly children: React.ReactNode };

// Tag is a short state label; the text always says the state, the color only
// repeats it.
export function Tag({ tone, children }: TagProps): React.JSX.Element {
  return (
    <span className={styles.tag} data-tone={tone}>
      {children}
    </span>
  );
}

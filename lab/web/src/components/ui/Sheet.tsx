import styles from './Sheet.module.css';

type SheetProps = React.HTMLAttributes<HTMLElement> & { readonly as?: 'section' | 'aside' | 'article' | 'div' };

// Sheet is the white card the panel's blocks sit on.
export function Sheet({ as: Element = 'div', className, ...props }: SheetProps): React.JSX.Element {
  return <Element {...props} className={[styles.sheet, className].filter(Boolean).join(' ')} />;
}

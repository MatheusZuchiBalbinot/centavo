import { Link } from 'react-router';
import type { LinkProps } from 'react-router';

import styles from './Button.module.css';

type Variant = 'primary' | 'secondary' | 'danger';
type Size = 'regular' | 'big';

type Look = { readonly variant?: Variant; readonly size?: Size };

type ButtonProps = Look & React.ButtonHTMLAttributes<HTMLButtonElement>;
type LinkButtonProps = Look & LinkProps;

export function Button({
  variant = 'secondary',
  size = 'regular',
  className,
  ...props
}: ButtonProps): React.JSX.Element {
  return <button type="button" {...props} className={classOf({ variant, size }, className)} />;
}

// LinkButton navigates, so it is a link that looks like a button.
export function LinkButton({
  variant = 'secondary',
  size = 'regular',
  className,
  ...props
}: LinkButtonProps): React.JSX.Element {
  return <Link {...props} className={classOf({ variant, size }, className)} />;
}

function classOf(look: Required<Look>, extra: string | undefined): string {
  return [styles.button, styles[look.variant], styles[look.size], extra].filter(Boolean).join(' ');
}

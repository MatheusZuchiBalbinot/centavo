import { Link, type LinkProps } from 'react-router';

type Variant = 'primary' | 'secondary' | 'danger';
type Size = 'regular' | 'large';

type Look = { readonly variant?: Variant; readonly size?: Size };

type ButtonProps = Look & React.ButtonHTMLAttributes<HTMLButtonElement>;
type LinkButtonProps = Look & LinkProps;

const VARIANT_CLASS: Readonly<Record<Variant, string>> = {
	primary: 'btn-primary',
	secondary: '',
	danger: 'btn-danger',
};
const SIZE_CLASS: Readonly<Record<Size, string>> = { regular: '', large: 'btn-lg' };

export function Button({ variant = 'secondary', size = 'regular', className, ...props }: ButtonProps): React.JSX.Element {
	return <button type="button" {...props} className={classOf({ variant, size }, className)} />;
}

// LinkButton navigates, so it is a link that looks like a button.
export function LinkButton({ variant = 'secondary', size = 'regular', className, ...props }: LinkButtonProps): React.JSX.Element {
	return <Link {...props} className={classOf({ variant, size }, className)} />;
}

function classOf(look: Required<Look>, extra: string | undefined): string {
	return ['btn', VARIANT_CLASS[look.variant], SIZE_CLASS[look.size], extra].filter(Boolean).join(' ');
}

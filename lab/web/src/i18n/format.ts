// A missing value keeps its placeholder, so a forgotten value shows instead
// of vanishing.
export function format(message: string, values: Readonly<Record<string, string | number>>): string {
	return message.replace(/\{(\w+)\}/g, (placeholder, name: string) => {
		const value = values[name];

		return value === undefined ? placeholder : String(value);
	});
}

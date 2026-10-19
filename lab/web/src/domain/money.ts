const CENTS_PER_REAL = 100;

const currency = new Intl.NumberFormat('pt-BR', { style: 'currency', currency: 'BRL' });

// formatCents writes an amount of cents in reais, "R$ 37,50".
export function formatCents(cents: number): string {
  return currency.format(cents / CENTS_PER_REAL);
}

import { useEffect } from 'react';

// useDocumentTitle names the browser tab after the page.
export function useDocumentTitle(title: string): void {
  useEffect(() => {
    document.title = title;
  }, [title]);
}

import { createBrowserRouter, RouterProvider } from 'react-router';
import type { RouteObject } from 'react-router';

import Layout from './components/Layout';
import HomePage from './pages/home/HomePage';
import NotFoundPage from './pages/not-found/NotFoundPage';
import { ROUTES } from './routes';

export const appRoutes: RouteObject[] = [
  {
    element: <Layout />,
    children: [
      { path: ROUTES.home, element: <HomePage /> },
      { path: '*', element: <NotFoundPage /> },
    ],
  },
];

const router = createBrowserRouter(appRoutes);

export default function App(): React.JSX.Element {
  return <RouterProvider router={router} />;
}

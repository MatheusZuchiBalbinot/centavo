import { createBrowserRouter, RouterProvider, type RouteObject } from 'react-router';

import Layout from './components/Layout';
import ComparePage from './pages/compare/ComparePage';
import HomePage from './pages/home/HomePage';
import LivePage from './pages/live/LivePage';
import NotFoundPage from './pages/not-found/NotFoundPage';
import RoundPage from './pages/round/RoundPage';
import RoundsPage from './pages/rounds/RoundsPage';
import { ROUTES } from './routes';

export const appRoutes: RouteObject[] = [
	{
		element: <Layout />,
		children: [
			{ path: ROUTES.home, element: <HomePage /> },
			{ path: ROUTES.live, element: <LivePage /> },
			{ path: ROUTES.rounds, element: <RoundsPage /> },
			{ path: ROUTES.round, element: <RoundPage /> },
			{ path: ROUTES.compare, element: <ComparePage /> },
			{ path: '*', element: <NotFoundPage /> },
		],
	},
];

const router = createBrowserRouter(appRoutes);

export default function App(): React.JSX.Element {
	return <RouterProvider router={router} />;
}

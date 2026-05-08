import React from 'react';
import ReactDOM from 'react-dom/client';
import { BrowserRouter, Routes, Route, Navigate } from 'react-router-dom';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';

import App from './App';
import Overview from './pages/Overview';
import ClusterDetail from './pages/ClusterDetail';
import './styles.css';

const queryClient = new QueryClient({
  defaultOptions: {
    queries: {
      // Pgao's collection cadence is 60s by default; refetch slightly faster
      // than that so the UI feels live without hammering the API.
      refetchInterval: 15_000,
      staleTime: 10_000,
      retry: 1,
    },
  },
});

ReactDOM.createRoot(document.getElementById('root')!).render(
  <React.StrictMode>
    <QueryClientProvider client={queryClient}>
      <BrowserRouter>
        <Routes>
          <Route path="/" element={<App />}>
            <Route index element={<Overview />} />
            <Route path="clusters/:id" element={<ClusterDetail />} />
            <Route path="*" element={<Navigate to="/" replace />} />
          </Route>
        </Routes>
      </BrowserRouter>
    </QueryClientProvider>
  </React.StrictMode>,
);

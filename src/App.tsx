import { useEffect } from "react";
import { Navigate, Route, Routes } from "react-router-dom";
import { initEngineBridge } from "./lib/engine";
import { useAuth } from "./lib/auth";
import Layout from "./components/Layout";
import Login from "./pages/Login";
import Dashboard from "./pages/Dashboard";
import NewJob from "./pages/NewJob";
import JobDetail from "./pages/JobDetail";
import Settings from "./pages/Settings";
import type { JSX } from "react";

function RequireAuth({ children }: { children: JSX.Element }) {
  const { isAuthed } = useAuth();
  return isAuthed ? children : <Navigate to="/login" replace />;
}

export default function App() {
  useEffect(() => {
    // Start listening to engine sidecar events as soon as the app mounts.
    initEngineBridge().catch((e) => console.error("engine bridge:", e));
  }, []);

  return (
    <Routes>
      <Route path="/login" element={<Login />} />
      <Route
        element={
          <RequireAuth>
            <Layout />
          </RequireAuth>
        }
      >
        <Route path="/" element={<Dashboard />} />
        <Route path="/jobs/new" element={<NewJob />} />
        <Route path="/jobs/:id" element={<JobDetail />} />
        <Route path="/settings" element={<Settings />} />
      </Route>
      <Route path="*" element={<Navigate to="/" replace />} />
    </Routes>
  );
}
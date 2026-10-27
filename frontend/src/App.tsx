import type { ReactNode } from "react";
import { Navigate, Route, Routes, useLocation } from "react-router-dom";
import { useAuth } from "./auth";
import Layout from "./components/Layout";
import { Loading } from "./components/ui";
import { ForgotPassword, Login, ResetPassword } from "./pages/AuthPages";
import Dashboard from "./pages/Dashboard";
import Contacts from "./pages/Contacts";
import Groups from "./pages/Groups";
import Templates from "./pages/Templates";
import Campaigns from "./pages/Campaigns";
import CampaignEditor from "./pages/CampaignEditor";
import CampaignReport from "./pages/CampaignReport";
import Users from "./pages/Users";
import SettingsPage from "./pages/Settings";
import Account from "./pages/Account";

function RequireAuth({ children, admin }: { children: ReactNode; admin?: boolean }) {
  const { user, loading } = useAuth();
  const loc = useLocation();
  if (loading) return <Loading />;
  if (!user) return <Navigate to="/login" state={{ from: loc.pathname }} replace />;
  if (admin && user.role !== "admin") return <Navigate to="/" replace />;
  return <>{children}</>;
}

export default function App() {
  return (
    <Routes>
      <Route path="/login" element={<Login />} />
      <Route path="/forgot-password" element={<ForgotPassword />} />
      <Route path="/reset-password" element={<ResetPassword />} />
      <Route
        element={
          <RequireAuth>
            <Layout />
          </RequireAuth>
        }
      >
        <Route index element={<Dashboard />} />
        <Route path="campaigns" element={<Campaigns />} />
        <Route path="campaigns/new" element={<CampaignEditor />} />
        <Route path="campaigns/:id/edit" element={<CampaignEditor />} />
        <Route path="campaigns/:id" element={<CampaignReport />} />
        <Route path="contacts" element={<Contacts />} />
        <Route path="groups" element={<Groups />} />
        <Route path="templates" element={<Templates />} />
        <Route path="account" element={<Account />} />
        <Route path="users" element={<RequireAuth admin><Users /></RequireAuth>} />
        <Route path="settings" element={<RequireAuth admin><SettingsPage /></RequireAuth>} />
      </Route>
      <Route path="*" element={<Navigate to="/" replace />} />
    </Routes>
  );
}

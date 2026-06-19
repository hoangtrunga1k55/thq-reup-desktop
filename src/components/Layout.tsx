import { NavLink, Outlet, useNavigate } from "react-router-dom";
import { useAuth } from "../lib/auth";

const nav = [
  { to: "/", label: "Bảng điều khiển", end: true },
  { to: "/jobs/new", label: "Tạo job mới" },
  { to: "/settings", label: "Cấu hình" },
  { to: "/keys", label: "API Keys" },
];

export default function Layout() {
  const { signOut } = useAuth();
  const navigate = useNavigate();

  return (
    <div className="flex h-screen bg-gray-50 text-gray-900">
      <aside className="flex w-60 flex-col border-r border-gray-200 bg-white">
        <div className="px-5 py-4 text-lg font-semibold">🎬 Auto ReUp</div>
        <nav className="flex-1 space-y-1 px-3">
          {nav.map((n) => (
            <NavLink
              key={n.to}
              to={n.to}
              end={n.end}
              className={({ isActive }) =>
                `block rounded-lg px-3 py-2 text-sm font-medium ${
                  isActive ? "bg-indigo-50 text-indigo-700" : "text-gray-600 hover:bg-gray-100"
                }`
              }
            >
              {n.label}
            </NavLink>
          ))}
        </nav>
        <button
          onClick={() => {
            signOut();
            navigate("/login");
          }}
          className="m-3 rounded-lg px-3 py-2 text-sm text-gray-500 hover:bg-gray-100"
        >
          Đăng xuất
        </button>
      </aside>
      <main className="flex-1 overflow-auto p-6">
        <Outlet />
      </main>
    </div>
  );
}
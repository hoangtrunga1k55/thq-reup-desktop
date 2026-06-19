import { useState } from "react";
import { useNavigate } from "react-router-dom";
import { getLicense, login } from "../lib/backend";
import { useAuth } from "../lib/auth";

export default function Login() {
  const { signIn } = useAuth();
  const navigate = useNavigate();
  const [email, setEmail] = useState("");
  const [password, setPassword] = useState("");
  const [error, setError] = useState("");
  const [loading, setLoading] = useState(false);

  async function onSubmit(e: React.FormEvent) {
    e.preventDefault();
    setError("");
    setLoading(true);
    try {
      const res = await login(email, password);
      signIn(res.token);
      // License is the SaaS control point — warn but don't hard-block in Phase 0.
      try {
        const lic = await getLicense();
        if (!lic.active) setError("Tài khoản chưa có license đang hoạt động.");
      } catch {
        /* ignore license probe errors in Phase 0 */
      }
      navigate("/");
    } catch (err) {
      setError(err instanceof Error ? err.message : "Đăng nhập thất bại");
    } finally {
      setLoading(false);
    }
  }

  return (
    <div className="flex h-screen items-center justify-center bg-gray-50">
      <form onSubmit={onSubmit} className="w-80 space-y-4 rounded-2xl bg-white p-8 shadow-sm">
        <div className="text-center text-xl font-semibold">🎬 Auto ReUp Studio</div>
        <input
          className="w-full rounded-lg border border-gray-300 px-3 py-2 text-sm"
          type="email"
          placeholder="Email"
          value={email}
          onChange={(e) => setEmail(e.target.value)}
          required
        />
        <input
          className="w-full rounded-lg border border-gray-300 px-3 py-2 text-sm"
          type="password"
          placeholder="Mật khẩu"
          value={password}
          onChange={(e) => setPassword(e.target.value)}
          required
        />
        {error && <div className="text-sm text-red-600">{error}</div>}
        <button
          type="submit"
          disabled={loading}
          className="w-full rounded-lg bg-indigo-600 py-2 text-sm font-medium text-white hover:bg-indigo-700 disabled:opacity-50"
        >
          {loading ? "Đang đăng nhập…" : "Đăng nhập"}
        </button>
      </form>
    </div>
  );
}
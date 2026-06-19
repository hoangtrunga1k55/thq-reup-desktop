import { useCallback, useEffect, useState } from "react";
import { Link } from "react-router-dom";
import { requestCommand } from "../lib/engine";

type Job = {
  id: string;
  source_url: string;
  status: string;
  current_step: string;
  progress: number;
  title: string;
  output_path: string;
  error: string;
  created_at: string;
  updated_at: string;
};

const STATUS_COLOR: Record<string, string> = {
  completed: "bg-green-100 text-green-700",
  failed: "bg-red-100 text-red-700",
  processing: "bg-blue-100 text-blue-700",
};

export default function Dashboard() {
  const [jobs, setJobs] = useState<Job[]>([]);
  const [error, setError] = useState("");
  const [loading, setLoading] = useState(true);

  const refresh = useCallback(async () => {
    setLoading(true);
    try {
      const res = await requestCommand<{ jobs: Job[] }>("list_jobs", {});
      setJobs(res.jobs ?? []);
      setError("");
    } catch (e) {
      setError(e instanceof Error ? e.message : "Không tải được lịch sử (engine chưa sẵn sàng?)");
    } finally {
      setLoading(false);
    }
  }, []);

  useEffect(() => {
    refresh();
  }, [refresh]);

  return (
    <div className="space-y-6">
      <div className="flex items-center justify-between">
        <h1 className="text-2xl font-semibold">Bảng điều khiển</h1>
        <div className="flex gap-2">
          <button onClick={refresh} className="rounded-lg border border-gray-300 px-3 py-2 text-sm hover:bg-gray-100">
            Làm mới
          </button>
          <Link to="/jobs/new" className="rounded-lg bg-indigo-600 px-4 py-2 text-sm font-medium text-white hover:bg-indigo-700">
            + Tạo job mới
          </Link>
        </div>
      </div>

      {error && <div className="rounded-lg bg-amber-50 p-3 text-sm text-amber-700">{error}</div>}

      {loading ? (
        <div className="text-gray-500">Đang tải…</div>
      ) : jobs.length === 0 ? (
        <div className="rounded-2xl border border-dashed border-gray-300 bg-white p-10 text-center text-gray-500">
          Chưa có job nào.
        </div>
      ) : (
        <div className="space-y-2">
          {jobs.map((j) => (
            <Link
              key={j.id}
              to={`/jobs/${j.id}`}
              className="flex items-center justify-between rounded-xl bg-white p-4 shadow-sm hover:shadow"
            >
              <div className="min-w-0">
                <div className="truncate font-medium">{j.title || j.source_url}</div>
                <div className="truncate text-xs text-gray-400">{j.source_url}</div>
              </div>
              <div className="flex items-center gap-3">
                <span className="text-xs text-gray-500">{j.progress}%</span>
                <span className={`rounded-full px-2 py-1 text-xs ${STATUS_COLOR[j.status] ?? "bg-gray-100 text-gray-600"}`}>
                  {j.status}
                </span>
              </div>
            </Link>
          ))}
        </div>
      )}
    </div>
  );
}
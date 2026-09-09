class Rook < Formula
  desc "TUI and agentic harness that builds a codebase from a markdown spec"
  homepage "https://github.com/rymawby/rook"
  url "https://github.com/rymawby/rook/archive/refs/tags/v0.1.0.tar.gz"
  sha256 "REPLACE_WITH_REAL_SHA256"
  license "MIT"
  head "https://github.com/rymawby/rook.git", branch: "main"

  depends_on "go" => :build

  def install
    system "go", "build", *std_go_args(ldflags: "-s -w"), "./cmd/rook"
  end

  test do
    system bin/"rook", "init"
    assert_path_exists testpath/"rook.json"
    assert_path_exists testpath/"SPEC.md"
  end
end

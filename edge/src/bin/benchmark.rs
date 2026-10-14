//! ListenLedger Edge — Acoustic Pipeline Benchmark CLI
//!
//! Evaluates audio framing, VAD thresholding, boundary detection, and anchor extraction
//! across synthetic acoustic scenarios and prints latency and accuracy diagnostics.

use listenledger_edge::audio::AudioConfig;
use listenledger_edge::benchmark::{run_acoustic_benchmark, standard_acoustic_scenarios};
use listenledger_edge::extractor::BoundaryConfig;

fn main() {
    println!("============================================================");
    println!("    ListenLedger Edge Acoustic Pipeline Benchmark Runner    ");
    println!("============================================================");

    let config = AudioConfig::default();
    let boundary_config = BoundaryConfig::default();
    let scenarios = standard_acoustic_scenarios();

    println!("Executing {} acoustic scenarios...", scenarios.len());
    let (metrics, results) = run_acoustic_benchmark(&scenarios, &config, &boundary_config);

    println!("\nScenario Results:");
    println!(
        "{:<30} {:<10} {:<15} {:<12}",
        "Scenario", "Result", "Captured Type", "Duration (us)"
    );
    println!("{:-<70}", "");
    for r in results {
        let status = if r.passed { "PASS" } else { "FAIL" };
        let cap_type = format!("{:?}", r.captured_type);
        println!(
            "{:<30} {:<10} {:<15} {:<12}",
            r.scenario_name, status, cap_type, r.total_duration_micros
        );
    }

    println!("\nAggregate Pipeline Performance:");
    println!(
        "  - Scenarios Passed:        {}/{}",
        metrics.passed_scenarios, metrics.total_scenarios
    );
    println!("  - False Positive Captures: {}", metrics.false_captures);
    println!("  - Missed Captures:         {}", metrics.missed_captures);
    println!(
        "  - Total Audio Frames:      {}",
        metrics.total_frames_processed
    );
    println!(
        "  - Avg Frame Latency:       {:.2} us",
        metrics.avg_frame_processing_micros
    );
    println!(
        "  - Max Frame Latency:       {} us",
        metrics.max_frame_processing_micros
    );

    if metrics.passed_scenarios == metrics.total_scenarios && metrics.false_captures == 0 {
        println!("\nAll edge acoustic pipeline gates PASSED.");
    } else {
        eprintln!("\nBenchmark FAILED: some scenarios did not meet criteria.");
        std::process::exit(1);
    }
}


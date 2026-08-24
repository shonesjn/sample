import os
import csv
import matplotlib
matplotlib.use('Agg')  # Prevent display errors on headless server/SSH
import matplotlib.pyplot as plt

def generate_chart():
    csv_file = "scheduler_metrics.csv"
    if not os.path.exists(csv_file):
        print(f"Error: {csv_file} does not exist. Run the scheduler to generate metrics first!")
        return

    stages = [
        "s1_client_to_sched_ms",
        "s2_sched_decision_ms",
        "s3_sched_to_compute_ms",
        "s4_compute_to_python_ms",
        "s5_python_decode_ms",
        "s6_yolo_inference_ms",
        "s7_python_to_compute_ms",
        "s8_compute_to_sched_ms",
        "s9_sched_to_frontend_ms"
    ]

    stage_sums = {s: 0.0 for s in stages}
    count = 0

    with open(csv_file, mode='r') as f:
        reader = csv.DictReader(f)
        for row in reader:
            try:
                # Only include rows where the tracking was active
                if float(row.get("end_to_end_latency_ms", 0.0)) > 0:
                    for s in stages:
                        stage_sums[s] += float(row.get(s, 0.0))
                    count += 1
            except (ValueError, TypeError):
                continue

    if count == 0:
        print("No active tracking data found in CSV yet. Ensure frames are streaming and scheduler is running.")
        return

    # Compute averages
    averages = {s: stage_sums[s] / count for s in stages}
    labels = [
        "S1: Client -> Sched\n(Network)",
        "S2: Decision\n(DQN)",
        "S3: Sched -> Compute\n(gRPC)",
        "S4: Compute -> Python\n(Local TCP)",
        "S5: Decode\n(JPEG)",
        "S6: Inference\n(YOLO GPU)",
        "S7: Python -> Compute\n(Local gRPC)",
        "S8: Compute -> Sched\n(gRPC)",
        "S9: Sched -> Client\n(Network)"
    ]
    values = [averages[s] for s in stages]

    # Plot
    plt.figure(figsize=(12, 6))
    colors = ['#4285F4', '#EA4335', '#FBBC05', '#34A853', '#8E24AA', '#F4511E', '#00ACC1', '#795548', '#3F51B5']
    bars = plt.bar(labels, values, color=colors, edgecolor='grey', width=0.6)

    # Add values on top of bars
    for bar in bars:
        yval = bar.get_height()
        plt.text(bar.get_x() + bar.get_width()/2.0, yval + 0.5, f"{yval:.2f} ms", ha='center', va='bottom', fontsize=9, fontweight='bold')

    plt.ylabel("Latency (ms)", fontsize=12, fontweight='bold')
    plt.title("Edge Offloading Communication & Inference Overheads (Stage-by-Stage)", fontsize=14, fontweight='bold', pad=15)
    plt.grid(axis='y', linestyle='--', alpha=0.5)
    plt.xticks(rotation=15, ha='right', fontsize=9)
    plt.tight_layout()

    output_img = "overhead_breakdown.png"
    plt.savefig(output_img, dpi=300)
    print(f"Chart successfully saved to {os.path.abspath(output_img)}!")

if __name__ == "__main__":
    generate_chart()

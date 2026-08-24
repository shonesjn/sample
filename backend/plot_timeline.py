import os
import pandas as pd
import numpy as np
import matplotlib
matplotlib.use('Agg')
import matplotlib.pyplot as plt

def generate_timeline_plot():
    csv_file = "scheduler_metrics.csv"
    if not os.path.exists(csv_file):
        print(f"Error: {csv_file} does not exist. Run the scheduler experiment first!")
        return

    df = pd.read_csv(csv_file)
    if df.empty:
        print("CSV file is empty.")
        return

    # Convert timestamp to datetime
    if df['timestamp'].iloc[0] > 1e11:
        df['datetime'] = pd.to_datetime(df['timestamp'], unit='ms')
    else:
        df['datetime'] = pd.to_datetime(df['timestamp'], unit='s')

    # Compute relative time in seconds
    start_time = df['datetime'].iloc[0]
    df['elapsed_sec'] = (df['datetime'] - start_time).dt.total_seconds()

    # Calculate per-second frame deltas for drop percentage
    df['total_delta'] = df['total_frames'].diff().fillna(0)
    df['dropped_delta'] = df['dropped_frames'].diff().fillna(0)

    # Calculate instantaneous Frame Drop Percentage (%)
    df['drop_rate_pct'] = np.where(df['total_delta'] > 0, (df['dropped_delta'] / df['total_delta']) * 100.0, 0.0)
    df['drop_rate_pct'] = df['drop_rate_pct'].clip(0.0, 100.0)

    # Calculate cumulative Frame Drop Percentage (%)
    df['cum_drop_rate_pct'] = np.where(df['total_frames'] > 0, (df['dropped_frames'] / df['total_frames']) * 100.0, 0.0)

    # Estimate active concurrent client streams (targeting ~15 FPS per stream)
    # Offered FPS = total_delta (incoming frames per second)
    df['active_streams'] = np.ceil(df['total_delta'] / 15.0).clip(lower=1)
    # Smooth active streams with rolling window
    df['active_streams_smooth'] = df['active_streams'].rolling(window=3, min_periods=1).mean()

    # Create 4-panel comprehensive timeline plot
    fig, (ax1, ax2, ax3, ax4) = plt.subplots(4, 1, figsize=(14, 12), sharex=True)
    fig.suptitle("Edge Multi-Node Scheduler: Capacity, Stream Support & Resource Timeline", fontsize=16, fontweight='bold', y=0.97)

    # Panel 1: Node Scheduling Allocation Timeline (FPS per Node)
    ax1.plot(df['elapsed_sec'], df['node1_throughput'], label='Node 1 (Orin Nano)', color='#1f77b4', linewidth=2)
    ax1.plot(df['elapsed_sec'], df['node2_throughput'], label='Node 2 (Jetson Nano)', color='#ff7f0e', linewidth=2)
    ax1.plot(df['elapsed_sec'], df['node3_throughput'], label='Node 3 (Remote GPU)', color='#2ca02c', linewidth=2)
    ax1.set_ylabel("Scheduled (FPS)", fontsize=10, fontweight='bold')
    ax1.set_title("1. Dynamic Node Scheduling Allocation (FPS per Node)", fontsize=11, fontweight='bold', loc='left')
    ax1.grid(True, linestyle='--', alpha=0.5)
    ax1.legend(loc='upper right', frameon=True, fontsize=9)

    # Panel 2: Supported Client Streams & Corresponding Frame Drop % (Dual Y-Axis)
    color_streams = '#6a0dad'
    color_drops = '#d62728'
    
    ax2.plot(df['elapsed_sec'], df['active_streams_smooth'], color=color_streams, linewidth=2.2, label='Active Supported Streams')
    ax2.set_ylabel("Active Client Streams", color=color_streams, fontsize=10, fontweight='bold')
    ax2.tick_params(axis='y', labelcolor=color_streams)
    ax2.set_title("2. Supported Client Streams vs. Frame Drop Rate (%)", fontsize=11, fontweight='bold', loc='left')
    ax2.grid(True, linestyle='--', alpha=0.5)

    ax2_drop = ax2.twinx()
    ax2_drop.plot(df['elapsed_sec'], df['drop_rate_pct'], color=color_drops, linewidth=1.5, linestyle='--', label='Instantaneous Drop %')
    ax2_drop.plot(df['elapsed_sec'], df['cum_drop_rate_pct'], color='#8c564b', linewidth=2, label='Cumulative Drop %')
    ax2_drop.set_ylabel("Frame Drop Rate (%)", color=color_drops, fontsize=10, fontweight='bold')
    ax2_drop.set_ylim(-5, 105)
    ax2_drop.tick_params(axis='y', labelcolor=color_drops)

    # Combine legends for ax2
    lines_1, labels_1 = ax2.get_legend_handles_labels()
    lines_2, labels_2 = ax2_drop.get_legend_handles_labels()
    ax2.legend(lines_1 + lines_2, labels_1 + labels_2, loc='upper right', frameon=True, fontsize=9)

    # Panel 3: GPU Utilization Status per Node (%)
    ax3.plot(df['elapsed_sec'], df['node1_gpu'], label='Node 1 GPU %', color='#1f77b4', linewidth=2)
    ax3.plot(df['elapsed_sec'], df['node2_gpu'], label='Node 2 GPU %', color='#ff7f0e', linewidth=2)
    ax3.plot(df['elapsed_sec'], df['node3_gpu'], label='Node 3 GPU %', color='#2ca02c', linewidth=2)
    ax3.set_ylabel("GPU Util (%)", fontsize=10, fontweight='bold')
    ax3.set_ylim(-5, 105)
    ax3.set_title("3. Hardware GPU Utilization Status per Node (%)", fontsize=11, fontweight='bold', loc='left')
    ax3.grid(True, linestyle='--', alpha=0.5)
    ax3.legend(loc='upper right', frameon=True, fontsize=9)

    # Panel 4: CPU Utilization Status per Node (%)
    if 'node1_cpu' in df.columns:
        ax4.plot(df['elapsed_sec'], df['node1_cpu'], label='Node 1 CPU %', color='#1f77b4', linewidth=2, linestyle='--')
        ax4.plot(df['elapsed_sec'], df['node2_cpu'], label='Node 2 CPU %', color='#ff7f0e', linewidth=2, linestyle='--')
        ax4.plot(df['elapsed_sec'], df['node3_cpu'], label='Node 3 CPU %', color='#2ca02c', linewidth=2, linestyle='--')
    else:
        ax4.plot(df['elapsed_sec'], df['cpu'], label='System CPU %', color='#d62728', linewidth=2)

    ax4.set_xlabel("Elapsed Experiment Time (seconds)", fontsize=11, fontweight='bold')
    ax4.set_ylabel("CPU Util (%)", fontsize=10, fontweight='bold')
    ax4.set_ylim(-5, 105)
    ax4.set_title("4. Hardware CPU Utilization Status per Node (%)", fontsize=11, fontweight='bold', loc='left')
    ax4.grid(True, linestyle='--', alpha=0.5)
    ax4.legend(loc='upper right', frameon=True, fontsize=9)

    plt.tight_layout()
    output_path = "node_selection_and_resource_timeline.png"
    plt.savefig(output_path, dpi=300)
    print(f"Timeline plot successfully saved to {os.path.abspath(output_path)}!")

    # Also generate Stream Support Capacity vs Drop Rate Summary Chart
    generate_capacity_summary(df)

def generate_capacity_summary(df):
    plt.figure(figsize=(10, 5))
    
    # Bin by active stream count
    stream_bins = df.groupby('active_streams').agg(
        avg_throughput=('throughput', 'mean'),
        avg_drop_pct=('drop_rate_pct', 'mean'),
        sample_count=('timestamp', 'count')
    ).reset_index()

    stream_bins = stream_bins[stream_bins['sample_count'] > 2] # Filter noise

    if stream_bins.empty:
        return

    x = stream_bins['active_streams']
    y_tp = stream_bins['avg_throughput']
    y_drop = stream_bins['avg_drop_pct']

    fig, ax1 = plt.subplots(figsize=(10, 5))
    color = '#1f77b4'
    ax1.set_xlabel('Supported Concurrent Client Streams', fontweight='bold', fontsize=11)
    ax1.set_ylabel('Processed Throughput (FPS)', color=color, fontweight='bold', fontsize=11)
    bars = ax1.bar(x - 0.2, y_tp, width=0.4, color=color, label='Processed FPS')
    ax1.tick_params(axis='y', labelcolor=color)
    ax1.grid(True, linestyle='--', alpha=0.5)

    ax2 = ax1.twinx()
    color = '#d62728'
    ax2.set_ylabel('Frame Drop Rate (%)', color=color, fontweight='bold', fontsize=11)
    lines = ax2.plot(x + 0.2, y_drop, color=color, marker='o', linewidth=2.5, label='Frame Drop %')
    ax2.set_ylim(-5, 105)
    ax2.tick_params(axis='y', labelcolor=color)

    plt.title("System Scaling: Supported Streams vs. Throughput & Drop Rate (%)", fontweight='bold', fontsize=13)
    fig.tight_layout()
    
    cap_path = "stream_capacity_vs_drop_rate.png"
    plt.savefig(cap_path, dpi=300)
    print(f"Capacity summary plot successfully saved to {os.path.abspath(cap_path)}!")

if __name__ == "__main__":
    generate_timeline_plot()

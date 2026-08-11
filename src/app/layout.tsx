import type { Metadata } from "next";
import "@/app/ui/globals.css";
import { geistSans, geistMono } from "@/app/ui/fonts";
import Sidebar from "@/framework/ui/Sidebar";
import ReactQueryProvider from "@framework/utils/ReactQueryProvider";

export const metadata: Metadata = {
  title: "Client Edge Tasks",
  description: "A collection of client edge tasks",
};

export default function RootLayout({
  children,
}: Readonly<{
  children: React.ReactNode;
}>) {
  return (
    <html lang="en">
      <body
        className={`${geistSans.variable} ${geistMono.variable} antialiased`}
      >
        <ReactQueryProvider>
          <div className="flex">
            <Sidebar />
            {children}
          </div>
        </ReactQueryProvider>
      </body>
    </html>
  );
}

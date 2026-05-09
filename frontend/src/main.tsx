import { StrictMode } from 'react'
import { createRoot } from 'react-dom/client'
import { BrowserRouter, Route, Routes } from 'react-router-dom'
import { Layout } from './components'
import Overview from './pages/Overview'
import Sessions from './pages/Sessions'
import SessionDetail from './pages/SessionDetail'
import Costs from './pages/Costs'
import Tools from './pages/Tools'
import Models from './pages/Models'
import Charts from './pages/Charts'

createRoot(document.getElementById('root')!).render(
  <StrictMode>
    <BrowserRouter>
      <Layout>
        <Routes>
          <Route path="/" element={<Overview />} />
          <Route path="/sessions" element={<Sessions />} />
          <Route path="/sessions/:id" element={<SessionDetail />} />
          <Route path="/costs" element={<Costs />} />
          <Route path="/tools" element={<Tools />} />
          <Route path="/models" element={<Models />} />
          <Route path="/charts" element={<Charts />} />
        </Routes>
      </Layout>
    </BrowserRouter>
  </StrictMode>,
)
